package model

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Project status values. Drafts are visible only to owner+admins until
// activated. Archived projects stay readable but accept no new posts;
// closed projects are wrapped up entirely.
const (
	ProjectStatusDraft    = "draft"
	ProjectStatusActive   = "active"
	ProjectStatusArchived = "archived"
	ProjectStatusClosed   = "closed"
)

// Project visibility values. Public is listable and viewable by every
// authenticated user. Members-only and hidden both require project
// membership to view; the practical difference (e.g. join requests vs
// invite-only) is layered on in Sprint 16c.
const (
	ProjectVisibilityPublic      = "public"
	ProjectVisibilityMembersOnly = "members_only"
	ProjectVisibilityHidden      = "hidden"
)

// Project is a structured container that lives inside a Space. It owns
// docs, seasons, members, and posts (via posts.season_id). The owner_id
// column is denormalised from project_members for fast access checks;
// ProjectStore.Create keeps both in sync, ownership transfer (16c) must
// update both in one transaction.
type Project struct {
	ID          int64      `json:"id"`
	SpaceID     int64      `json:"space_id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Visibility  string     `json:"visibility"`
	OwnerID     int64      `json:"owner_id"`
	Icon        string     `json:"icon"`
	Color       string     `json:"color"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Joined / on-demand fields, not populated by the default SELECT.
	SpaceSlug string `json:"space_slug,omitempty"`
	SpaceName string `json:"space_name,omitempty"`
	Tags      []Tag  `json:"tags,omitempty"`
}

type ProjectStore struct {
	DB *pgxpool.Pool
}

// Sentinel errors. Handlers map these to specific HTTP status codes;
// any other error from the store is treated as a 500 with the wrapped
// context discarded from the user-facing response.
var (
	ErrProjectInvalidSlug = errors.New("project slug invalid format")
	ErrProjectSlugTaken   = errors.New("project slug already taken in this space")
	ErrProjectNotFound    = errors.New("project not found")
)

// projectSelectCols / projectJoins keep the column list in sync between
// FindByID, FindBySlug, ListBySpace, and the scan helpers below. Adding
// a column to the table means touching exactly these constants.
const projectSelectCols = `
	p.id, p.space_id, p.slug, p.name, p.description, p.status, p.visibility,
	p.owner_id, p.icon, p.color, p.deleted_at, p.created_at, p.updated_at,
	COALESCE(s.slug, ''), COALESCE(s.name, '')`

const projectJoins = `
	FROM projects p
	LEFT JOIN spaces s ON p.space_id = s.id`

var projectSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateProjectSlug enforces the URL-safe slug format. Slugs sit in
// /spaces/<space_slug>/projects/<project_slug> in 16b, so they share
// the same character class as space slugs.
func ValidateProjectSlug(slug string) error {
	if len(slug) < 3 || len(slug) > 64 {
		return ErrProjectInvalidSlug
	}
	if !projectSlugRe.MatchString(slug) {
		return ErrProjectInvalidSlug
	}
	return nil
}

// SlugifyProject derives a slug from a free-form name. Same rules as
// ValidateProjectSlug accepts; callers should still pass the result
// through ValidateProjectSlug to catch edge cases (empty input, etc.).
func SlugifyProject(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = projectSlugCleanRe.ReplaceAllString(s, "-")
	s = projectSlugDashesRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
		s = strings.TrimRight(s, "-")
	}
	return s
}

var (
	projectSlugCleanRe  = regexp.MustCompile(`[^a-z0-9]+`)
	projectSlugDashesRe = regexp.MustCompile(`-+`)
)

// CreateParams bundles the required + optional fields for a new
// project. Status defaults to draft; visibility defaults to public.
type ProjectCreateParams struct {
	SpaceID     int64
	Slug        string
	Name        string
	Description string
	Status      string
	Visibility  string
	OwnerID     int64
	Icon        string
	Color       string
}

// Create inserts a project row and the matching owner project_members
// row in one transaction. Returns ErrProjectInvalidSlug if the slug
// fails ValidateProjectSlug, or ErrProjectSlugTaken if another live
// project in the same space already uses it.
func (s *ProjectStore) Create(ctx context.Context, p ProjectCreateParams) (*Project, error) {
	if err := ValidateProjectSlug(p.Slug); err != nil {
		return nil, err
	}
	if p.Status == "" {
		p.Status = ProjectStatusDraft
	}
	if p.Visibility == "" {
		p.Visibility = ProjectVisibilityPublic
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create project: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// ON CONFLICT against the partial unique index (space_id, slug)
	// WHERE deleted_at IS NULL. The index predicate is repeated in the
	// conflict_target so Postgres matches the right index. When the
	// row already exists the INSERT is a no-op and RETURNING gives
	// pgx.ErrNoRows back, which we map to ErrProjectSlugTaken.
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO projects
			(space_id, slug, name, description, status, visibility,
			 owner_id, icon, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (space_id, slug) WHERE deleted_at IS NULL DO NOTHING
		RETURNING id`,
		p.SpaceID, p.Slug, p.Name, p.Description, p.Status, p.Visibility,
		p.OwnerID, p.Icon, p.Color,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return nil, ErrProjectSlugTaken
	}
	if err != nil {
		return nil, fmt.Errorf("create project: insert: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, invited_by)
		VALUES ($1, $2, 'owner', NULL)`,
		id, p.OwnerID,
	); err != nil {
		return nil, fmt.Errorf("create project: insert owner member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create project: commit: %w", err)
	}

	return s.FindByID(ctx, id)
}

// FindByID returns the project with all joined fields populated, or
// (nil, nil) if no live row matches.
func (s *ProjectStore) FindByID(ctx context.Context, id int64) (*Project, error) {
	p := &Project{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+projectSelectCols+projectJoins+
			` WHERE p.id = $1 AND p.deleted_at IS NULL`, id,
	).Scan(
		&p.ID, &p.SpaceID, &p.Slug, &p.Name, &p.Description, &p.Status,
		&p.Visibility, &p.OwnerID, &p.Icon, &p.Color, &p.DeletedAt,
		&p.CreatedAt, &p.UpdatedAt, &p.SpaceSlug, &p.SpaceName,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	return p, nil
}

// FindBySlug looks the project up by space + slug. Returns (nil, nil)
// when no live row matches; the partial unique index on
// (space_id, slug) WHERE deleted_at IS NULL guarantees there is at
// most one live match.
func (s *ProjectStore) FindBySlug(ctx context.Context, spaceID int64, slug string) (*Project, error) {
	p := &Project{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+projectSelectCols+projectJoins+
			` WHERE p.space_id = $1 AND p.slug = $2 AND p.deleted_at IS NULL`,
		spaceID, slug,
	).Scan(
		&p.ID, &p.SpaceID, &p.Slug, &p.Name, &p.Description, &p.Status,
		&p.Visibility, &p.OwnerID, &p.Icon, &p.Color, &p.DeletedAt,
		&p.CreatedAt, &p.UpdatedAt, &p.SpaceSlug, &p.SpaceName,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find project by slug: %w", err)
	}
	return p, nil
}

// ListBySpace returns the projects in a space the viewer is allowed to
// see. Public projects are always included; members-only and hidden
// projects are included only when the viewer has a project_members
// row. viewerID == 0 is treated as anonymous (only public matches).
func (s *ProjectStore) ListBySpace(ctx context.Context, spaceID, viewerID int64) ([]Project, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+projectSelectCols+projectJoins+`
		 WHERE p.space_id = $1
		   AND p.deleted_at IS NULL
		   AND (
		       p.visibility = 'public'
		       OR EXISTS (
		           SELECT 1 FROM project_members pm
		           WHERE pm.project_id = p.id AND pm.user_id = $2
		       )
		   )
		 ORDER BY p.created_at DESC`,
		spaceID, viewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects by space: %w", err)
	}
	defer rows.Close()

	return scanProjects(rows)
}

// ListByOwner returns every live project owned by the user, regardless
// of visibility. Used by /api/users/me/projects in 16b.
func (s *ProjectStore) ListByOwner(ctx context.Context, ownerID int64) ([]Project, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+projectSelectCols+projectJoins+`
		 WHERE p.owner_id = $1 AND p.deleted_at IS NULL
		 ORDER BY p.created_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects by owner: %w", err)
	}
	defer rows.Close()
	return scanProjects(rows)
}

// ProjectUpdateParams covers every column that PATCH /projects/:id is
// allowed to change. Slug is intentionally immutable (URL stability),
// space_id is immutable (a project belongs to one space for life), and
// owner_id changes go through a separate transfer flow.
type ProjectUpdateParams struct {
	ID          int64
	Name        string
	Description string
	Status      string
	Visibility  string
	Icon        string
	Color       string
}

// Update writes the mutable columns. Returns ErrProjectNotFound when
// no live row matches the id. Caller verifies the actor's permission
// to mutate before calling.
func (s *ProjectStore) Update(ctx context.Context, p ProjectUpdateParams) error {
	res, err := s.DB.Exec(ctx, `
		UPDATE projects
		   SET name = $2, description = $3, status = $4, visibility = $5,
		       icon = $6, color = $7, updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`,
		p.ID, p.Name, p.Description, p.Status, p.Visibility, p.Icon, p.Color,
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// SoftDelete sets deleted_at. Existing posts assigned to the project's
// seasons keep their season_id; admins can recover the project later
// by clearing deleted_at directly in SQL until a recovery endpoint
// lands.
func (s *ProjectStore) SoftDelete(ctx context.Context, id int64) error {
	res, err := s.DB.Exec(ctx,
		`UPDATE projects SET deleted_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("soft-delete project: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// CanUserAccess returns true when the viewer can read the project
// given its visibility. Public is always readable, the owner always
// counts, otherwise we look for a project_members row.
//
// viewerID == 0 means anonymous; in 16a anonymous read access is not
// wired up (handlers gate on RequireAuth) but the store stays honest
// for 16b where public read may go anonymous.
func (s *ProjectStore) CanUserAccess(ctx context.Context, projectID, viewerID int64) (bool, error) {
	var visibility string
	var ownerID int64
	err := s.DB.QueryRow(ctx,
		`SELECT visibility, owner_id FROM projects
		 WHERE id = $1 AND deleted_at IS NULL`, projectID,
	).Scan(&visibility, &ownerID)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check project access: %w", err)
	}

	if visibility == ProjectVisibilityPublic {
		return true, nil
	}
	if viewerID != 0 && ownerID == viewerID {
		return true, nil
	}
	if viewerID == 0 {
		return false, nil
	}

	var member bool
	err = s.DB.QueryRow(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM project_members
		    WHERE project_id = $1 AND user_id = $2
		 )`, projectID, viewerID,
	).Scan(&member)
	if err != nil {
		return false, fmt.Errorf("check project membership: %w", err)
	}
	return member, nil
}

// AttachTags links project_id to each tag id in tagIDs. Duplicates are
// silently ignored. Used by the create handler after ProjectStore.Create
// returns. tag_id values that don't exist in tags raise an FK error.
func (s *ProjectStore) AttachTags(ctx context.Context, projectID int64, tagIDs []int64) error {
	if len(tagIDs) == 0 {
		return nil
	}
	for _, id := range tagIDs {
		if _, err := s.DB.Exec(ctx,
			`INSERT INTO project_tags (project_id, tag_id)
			 VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			projectID, id,
		); err != nil {
			return fmt.Errorf("attach project tag %d: %w", id, err)
		}
	}
	return nil
}

// ListTags returns the tags currently attached to a project, ordered
// by use_count for consistency with PostStore tag joins.
func (s *ProjectStore) ListTags(ctx context.Context, projectID int64) ([]Tag, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.use_count, t.created_by, t.created_at
		FROM tags t
		JOIN project_tags pt ON pt.tag_id = t.id
		WHERE pt.project_id = $1
		ORDER BY t.use_count DESC, t.slug ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project tags: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

func scanProjects(rows pgx.Rows) ([]Project, error) {
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(
			&p.ID, &p.SpaceID, &p.Slug, &p.Name, &p.Description, &p.Status,
			&p.Visibility, &p.OwnerID, &p.Icon, &p.Color, &p.DeletedAt,
			&p.CreatedAt, &p.UpdatedAt, &p.SpaceSlug, &p.SpaceName,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return out, nil
}
