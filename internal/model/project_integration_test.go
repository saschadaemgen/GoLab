//go:build integration

package model

// Sprint 16a integration tests for the project system store layer.
// They need a real Postgres instance with migrations applied through
// 029 and the default 'meta' space + at least one user (id=1) in
// place. Run with:
//
//   GOLAB_TEST_DB=postgres://golab:golab-dev@127.0.0.1:5432/golab_test?sslmode=disable \
//     go test -tags integration ./internal/model/...
//
// Each test calls resetProjectTables in its setup so the suite is
// repeatable. The reset truncates only the five project_* tables and
// clears posts.season_id; existing users / spaces are left intact.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type projectSuite struct {
	pool       *pgxpool.Pool
	projects   *ProjectStore
	docs       *ProjectDocStore
	seasons    *SeasonStore
	members    *ProjectMemberStore
	spaceID    int64
	altSpaceID int64
	userID     int64
	otherID    int64
}

func setupProjectSuite(t *testing.T) *projectSuite {
	t.Helper()
	dsn := os.Getenv("GOLAB_TEST_DB")
	if dsn == "" {
		t.Skip("GOLAB_TEST_DB not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	s := &projectSuite{
		pool:     pool,
		projects: &ProjectStore{DB: pool},
		docs:     &ProjectDocStore{DB: pool},
		seasons:  &SeasonStore{DB: pool},
		members:  &ProjectMemberStore{DB: pool},
	}

	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT id FROM spaces WHERE slug = 'meta'`).Scan(&s.spaceID); err != nil {
		t.Skipf("'meta' space missing in test DB: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM spaces WHERE slug = 'simplex'`).Scan(&s.altSpaceID); err != nil {
		t.Skipf("'simplex' space missing in test DB: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&s.userID); err != nil {
		t.Skipf("test DB needs at least one user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE id <> $1 ORDER BY id LIMIT 1`,
		s.userID).Scan(&s.otherID); err != nil {
		t.Skipf("test DB needs a second user for visibility tests: %v", err)
	}

	resetProjectTables(t, pool)
	return s
}

func resetProjectTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE posts SET season_id = NULL WHERE season_id IS NOT NULL`); err != nil {
		t.Fatalf("reset posts.season_id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`TRUNCATE project_tags, project_members, project_docs, seasons, projects
		 RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate project tables: %v", err)
	}
}

func (s *projectSuite) mustCreate(t *testing.T, slug, name string) *Project {
	t.Helper()
	p, err := s.projects.Create(context.Background(), ProjectCreateParams{
		SpaceID: s.spaceID,
		Slug:    slug,
		Name:    name,
		OwnerID: s.userID,
	})
	if err != nil {
		t.Fatalf("Create(%q): %v", slug, err)
	}
	return p
}

// ============================================================
// ProjectStore behaviour
// ============================================================

func TestProjectStore_CreateSucceeds(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "trust-engine", "Trust Engine")
	if p.ID == 0 {
		t.Fatal("Create did not assign id")
	}
	if p.Slug != "trust-engine" {
		t.Errorf("Slug = %q, want trust-engine", p.Slug)
	}
	if p.Status != ProjectStatusDraft {
		t.Errorf("default Status = %q, want draft", p.Status)
	}
	if p.Visibility != ProjectVisibilityPublic {
		t.Errorf("default Visibility = %q, want public", p.Visibility)
	}
}

func TestProjectStore_CreateRejectsDuplicateSlugInSameSpace(t *testing.T) {
	s := setupProjectSuite(t)
	s.mustCreate(t, "dup-slug", "First")
	_, err := s.projects.Create(context.Background(), ProjectCreateParams{
		SpaceID: s.spaceID,
		Slug:    "dup-slug",
		Name:    "Second",
		OwnerID: s.userID,
	})
	if !errors.Is(err, ErrProjectSlugTaken) {
		t.Fatalf("Create returned %v, want ErrProjectSlugTaken", err)
	}
}

func TestProjectStore_CreateAllowsSameSlugInDifferentSpaces(t *testing.T) {
	s := setupProjectSuite(t)
	s.mustCreate(t, "shared-slug", "In meta")
	p2, err := s.projects.Create(context.Background(), ProjectCreateParams{
		SpaceID: s.altSpaceID,
		Slug:    "shared-slug",
		Name:    "In simplex",
		OwnerID: s.userID,
	})
	if err != nil {
		t.Fatalf("Create across spaces: %v", err)
	}
	if p2.SpaceID != s.altSpaceID {
		t.Errorf("p2.SpaceID = %d, want %d", p2.SpaceID, s.altSpaceID)
	}
}

func TestProjectStore_CreateInsertsOwnerMember(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "owner-row", "Owner Row")
	role, err := s.members.GetRole(context.Background(), p.ID, s.userID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role != ProjectRoleOwner {
		t.Errorf("owner role = %q, want owner", role)
	}
}

func TestProjectStore_SoftDeleteHidesFromFindByID(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "to-delete", "To Delete")
	if err := s.projects.SoftDelete(context.Background(), p.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	found, err := s.projects.FindByID(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("FindByID after SoftDelete: %v", err)
	}
	if found != nil {
		t.Errorf("FindByID returned %+v, want nil for soft-deleted row", found)
	}
}

func TestProjectStore_ListBySpaceVisibility(t *testing.T) {
	s := setupProjectSuite(t)
	pub := s.mustCreate(t, "public-one", "Public")
	hidden, err := s.projects.Create(context.Background(), ProjectCreateParams{
		SpaceID:    s.spaceID,
		Slug:       "hidden-one",
		Name:       "Hidden",
		OwnerID:    s.userID,
		Visibility: ProjectVisibilityHidden,
	})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}

	// Non-member viewer sees only the public project.
	list, err := s.projects.ListBySpace(context.Background(), s.spaceID, s.otherID)
	if err != nil {
		t.Fatalf("ListBySpace non-member: %v", err)
	}
	if !containsProject(list, pub.ID) {
		t.Errorf("non-member list missing public project")
	}
	if containsProject(list, hidden.ID) {
		t.Errorf("non-member list includes hidden project")
	}

	// Add otherID as a viewer; they should now see the hidden project.
	if err := s.members.Add(context.Background(), hidden.ID, s.otherID,
		ProjectRoleViewer, s.userID); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	list, err = s.projects.ListBySpace(context.Background(), s.spaceID, s.otherID)
	if err != nil {
		t.Fatalf("ListBySpace member: %v", err)
	}
	if !containsProject(list, hidden.ID) {
		t.Errorf("member list missing hidden project")
	}
}

func TestProjectStore_CanUserAccess(t *testing.T) {
	s := setupProjectSuite(t)
	pub := s.mustCreate(t, "pub-access", "Pub")
	priv, err := s.projects.Create(context.Background(), ProjectCreateParams{
		SpaceID:    s.spaceID,
		Slug:       "priv-access",
		Name:       "Priv",
		OwnerID:    s.userID,
		Visibility: ProjectVisibilityHidden,
	})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}

	cases := []struct {
		name    string
		project int64
		viewer  int64
		want    bool
	}{
		{"public-anon", pub.ID, 0, true},
		{"public-other", pub.ID, s.otherID, true},
		{"hidden-anon", priv.ID, 0, false},
		{"hidden-non-member", priv.ID, s.otherID, false},
		{"hidden-owner", priv.ID, s.userID, true},
	}
	for _, c := range cases {
		got, err := s.projects.CanUserAccess(context.Background(), c.project, c.viewer)
		if err != nil {
			t.Errorf("%s: CanUserAccess: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: CanUserAccess = %v, want %v", c.name, got, c.want)
		}
	}
}

// ============================================================
// ProjectDocStore behaviour
// ============================================================

func TestProjectDocStore_UpsertCreatesAndUpdates(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "doc-test", "Doc Test")

	first, err := s.docs.Upsert(context.Background(), ProjectDocUpsertParams{
		ProjectID:   p.ID,
		DocType:     ProjectDocConcept,
		Title:       "Concept v1",
		ContentMD:   "# v1",
		ContentHTML: "<h1>v1</h1>",
		EditedBy:    s.userID,
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	second, err := s.docs.Upsert(context.Background(), ProjectDocUpsertParams{
		ProjectID:   p.ID,
		DocType:     ProjectDocConcept,
		Title:       "Concept v2",
		ContentMD:   "# v2",
		ContentHTML: "<h1>v2</h1>",
		EditedBy:    s.userID,
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("Upsert created a new row (id %d -> %d), want UPDATE on conflict",
			first.ID, second.ID)
	}
	if second.Title != "Concept v2" {
		t.Errorf("Title = %q, want updated value", second.Title)
	}
}

func TestProjectDocStore_AllowsMultipleCustomDocs(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "custom-test", "Custom Test")

	a, err := s.docs.Upsert(context.Background(), ProjectDocUpsertParams{
		ProjectID: p.ID,
		DocType:   ProjectDocCustom,
		Title:     "ADR 1",
		ContentMD: "first",
		EditedBy:  s.userID,
	})
	if err != nil {
		t.Fatalf("custom 1: %v", err)
	}
	b, err := s.docs.Upsert(context.Background(), ProjectDocUpsertParams{
		ProjectID: p.ID,
		DocType:   ProjectDocCustom,
		Title:     "ADR 2",
		ContentMD: "second",
		EditedBy:  s.userID,
	})
	if err != nil {
		t.Fatalf("custom 2: %v", err)
	}
	if a.ID == b.ID {
		t.Errorf("two custom docs got the same id; partial unique index leaking")
	}
}

// ============================================================
// SeasonStore behaviour
// ============================================================

func TestSeasonStore_SequentialNumbering(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "season-test", "Season Test")
	for i := 1; i <= 3; i++ {
		se, err := s.seasons.Create(context.Background(), SeasonCreateParams{
			ProjectID: p.ID,
			Title:     "Season",
		})
		if err != nil {
			t.Fatalf("create season %d: %v", i, err)
		}
		if se.SeasonNumber != i {
			t.Errorf("season %d got number %d, want %d", i, se.SeasonNumber, i)
		}
	}
}

func TestSeasonStore_ActivateAndCloseTransitions(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "season-states", "Season States")
	se, err := s.seasons.Create(context.Background(), SeasonCreateParams{
		ProjectID: p.ID,
		Title:     "S1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Close before activate must fail.
	if err := s.seasons.Close(context.Background(), se.ID, "doc", "<p>doc</p>"); !errors.Is(err, ErrSeasonNotActive) {
		t.Errorf("Close on planned: got %v, want ErrSeasonNotActive", err)
	}

	if err := s.seasons.Activate(context.Background(), se.ID); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	// Activate twice must fail.
	if err := s.seasons.Activate(context.Background(), se.ID); !errors.Is(err, ErrSeasonNotPlanned) {
		t.Errorf("re-Activate: got %v, want ErrSeasonNotPlanned", err)
	}

	if err := s.seasons.Close(context.Background(), se.ID,
		"# Wrap-up", "<h1>Wrap-up</h1>"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed, err := s.seasons.FindByID(context.Background(), se.ID)
	if err != nil || closed == nil {
		t.Fatalf("reload: err=%v season=%v", err, closed)
	}
	if closed.Status != SeasonStatusClosed {
		t.Errorf("Status = %q, want closed", closed.Status)
	}
	if closed.ClosingDocHTML != "<h1>Wrap-up</h1>" {
		t.Errorf("ClosingDocHTML not stored: %q", closed.ClosingDocHTML)
	}

	// Close twice must fail.
	if err := s.seasons.Close(context.Background(), se.ID, "x", "<p>x</p>"); !errors.Is(err, ErrSeasonNotActive) {
		t.Errorf("re-Close: got %v, want ErrSeasonNotActive", err)
	}
}

// ============================================================
// ProjectMemberStore behaviour
// ============================================================

func TestProjectMemberStore_AddRejectsDuplicate(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "member-test", "Member Test")
	if err := s.members.Add(context.Background(), p.ID, s.otherID,
		ProjectRoleContributor, s.userID); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := s.members.Add(context.Background(), p.ID, s.otherID,
		ProjectRoleContributor, s.userID)
	if !errors.Is(err, ErrMemberAlreadyExists) {
		t.Errorf("duplicate Add: got %v, want ErrMemberAlreadyExists", err)
	}
}

func TestProjectMemberStore_RemoveRejectsOwner(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "owner-protected", "Owner Protected")
	err := s.members.Remove(context.Background(), p.ID, s.userID)
	if !errors.Is(err, ErrCannotRemoveOwner) {
		t.Errorf("Remove owner: got %v, want ErrCannotRemoveOwner", err)
	}
}

func TestProjectMemberStore_UpdateRoleRejectsOwner(t *testing.T) {
	s := setupProjectSuite(t)
	p := s.mustCreate(t, "role-test", "Role Test")
	if err := s.members.Add(context.Background(), p.ID, s.otherID,
		ProjectRoleViewer, s.userID); err != nil {
		t.Fatalf("seed Add: %v", err)
	}
	err := s.members.UpdateRole(context.Background(), p.ID, s.otherID,
		ProjectRoleOwner)
	if !errors.Is(err, ErrCannotAssignOwner) {
		t.Errorf("UpdateRole=owner: got %v, want ErrCannotAssignOwner", err)
	}
}

// ============================================================
// helpers
// ============================================================

func containsProject(ps []Project, id int64) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}
