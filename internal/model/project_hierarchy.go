package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Sprint 16d: parent-child project hierarchy. The depth is capped at
// one in the application layer (a sub-project cannot itself become a
// parent), so ListChildProjects on a sub-project is guaranteed to
// return zero rows. Same-space-only is enforced by the validation
// methods below.
//
// Sentinel errors that the form / API handlers translate into
// 4xx responses with friendly copy.
var (
	ErrParentInvalid      = errors.New("parent project invalid")
	ErrParentSameSpace    = errors.New("parent must live in the same space")
	ErrParentNotTopLevel  = errors.New("a sub-project cannot itself be a parent")
	ErrParentSelfReference = errors.New("a project cannot be its own parent")
	ErrProjectHasChildren = errors.New("project has sub-projects; cannot itself become a sub-project")
)

// ProjectWithParent bundles a project with its (optional) parent for
// breadcrumb / header rendering. Parent stays nil for top-level
// projects so templates branch on `{{ if .Parent }}`.
type ProjectWithParent struct {
	*Project
	Parent *Project
}

// ListChildProjects returns the sub-projects of a parent the viewer
// is allowed to see. Visibility filter mirrors ListBySpace: public
// always, members-only and hidden when the viewer has a
// project_members row on the child.
func (s *ProjectStore) ListChildProjects(ctx context.Context, parentID, viewerID int64) ([]Project, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+projectSelectCols+projectJoins+`
		 WHERE p.parent_project_id = $1
		   AND p.deleted_at IS NULL
		   AND (
		       p.visibility = 'public'
		       OR EXISTS (
		           SELECT 1 FROM project_members pm
		            WHERE pm.project_id = p.id AND pm.user_id = $2
		       )
		   )
		 ORDER BY p.created_at DESC`,
		parentID, viewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list child projects: %w", err)
	}
	defer rows.Close()
	return scanProjects(rows)
}

// CountChildProjects returns how many sub-projects of `parentID`
// the viewer can see. Cheap query so handlers can decide whether to
// render the sub-projects section without paying for the full list.
func (s *ProjectStore) CountChildProjects(ctx context.Context, parentID, viewerID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM projects p
		  WHERE p.parent_project_id = $1
		    AND p.deleted_at IS NULL
		    AND (
		        p.visibility = 'public'
		        OR EXISTS (
		            SELECT 1 FROM project_members pm
		             WHERE pm.project_id = p.id AND pm.user_id = $2
		        )
		    )`,
		parentID, viewerID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count child projects: %w", err)
	}
	return n, nil
}

// HasOwnChildren returns true when the project has at least one
// non-deleted sub-project. Used by the edit form to reject setting a
// parent on a project that itself has children (would push depth > 1).
// Visibility-agnostic - if the rows exist, depth-cap applies regardless
// of who can see them.
func (s *ProjectStore) HasOwnChildren(ctx context.Context, projectID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM projects
		     WHERE parent_project_id = $1 AND deleted_at IS NULL
		 )`, projectID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check has-own-children: %w", err)
	}
	return exists, nil
}

// ValidateParent checks whether the candidate parent_project_id is
// usable for a (new or existing) project living in `spaceID`. It is
// the single integration point handlers call before INSERT/UPDATE,
// so the depth-1 + same-space + not-self + parent-visible rules are
// in one place.
//
// `selfID` is the project being edited; pass 0 on Create. The check
// exists so the edit form can reject "make this project its own
// parent" cleanly.
//
// Returns one of the Err* sentinels above when the candidate is
// invalid, nil otherwise.
func (s *ProjectStore) ValidateParent(ctx context.Context, parentID, spaceID, selfID, viewerID int64) error {
	if parentID <= 0 {
		return ErrParentInvalid
	}
	if parentID == selfID {
		return ErrParentSelfReference
	}

	var (
		parentSpace        int64
		parentParentID     *int64
		parentDeletedAt    *string
		parentVisibility   string
		parentOwnerID      int64
	)
	err := s.DB.QueryRow(ctx,
		`SELECT space_id, parent_project_id, deleted_at::text,
		        visibility, owner_id
		   FROM projects WHERE id = $1`, parentID,
	).Scan(&parentSpace, &parentParentID, &parentDeletedAt,
		&parentVisibility, &parentOwnerID)
	if err == pgx.ErrNoRows {
		return ErrParentInvalid
	}
	if err != nil {
		return fmt.Errorf("validate parent: lookup: %w", err)
	}
	if parentDeletedAt != nil {
		return ErrParentInvalid
	}
	if parentSpace != spaceID {
		return ErrParentSameSpace
	}
	if parentParentID != nil {
		return ErrParentNotTopLevel
	}

	// Visibility check: the user must be able to see the parent so
	// they can't smuggle a sub-project under a hidden project they
	// don't belong to. Reuse CanUserAccess so the rules stay
	// consistent.
	if viewerID != 0 {
		ok, err := s.CanUserAccess(ctx, parentID, viewerID)
		if err != nil {
			return fmt.Errorf("validate parent: access: %w", err)
		}
		if !ok {
			return ErrParentInvalid
		}
	}

	return nil
}

// ListPotentialParents returns the top-level projects in the space
// that the user can see, suitable for populating the "parent project"
// dropdown on the create/edit forms. `excludeID` is the project being
// edited (pass 0 on Create); it is dropped from the list so a project
// can't pick itself.
func (s *ProjectStore) ListPotentialParents(ctx context.Context, spaceID, viewerID, excludeID int64) ([]Project, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+projectSelectCols+projectJoins+`
		 WHERE p.space_id = $1
		   AND p.deleted_at IS NULL
		   AND p.parent_project_id IS NULL
		   AND p.id <> $3
		   AND (
		       p.visibility = 'public'
		       OR EXISTS (
		           SELECT 1 FROM project_members pm
		            WHERE pm.project_id = p.id AND pm.user_id = $2
		       )
		   )
		 ORDER BY p.name`,
		spaceID, viewerID, excludeID,
	)
	if err != nil {
		return nil, fmt.Errorf("list potential parents: %w", err)
	}
	defer rows.Close()
	return scanProjects(rows)
}

// GetWithParent loads a project plus its parent (if any) in two
// queries (one for the project, one for the parent when needed).
// Returns nil, nil when the project is missing or soft-deleted.
func (s *ProjectStore) GetWithParent(ctx context.Context, projectID int64) (*ProjectWithParent, error) {
	p, err := s.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	out := &ProjectWithParent{Project: p}
	if p.ParentProjectID != nil {
		parent, err := s.FindByID(ctx, *p.ParentProjectID)
		if err != nil {
			return nil, err
		}
		// parent may be nil if it was soft-deleted; the breadcrumb
		// then surfaces the orphan state via ParentSlug/ParentName
		// being whatever the join produced (likely empty for a
		// soft-deleted parent because FindByID filters deleted).
		out.Parent = parent
	}
	return out, nil
}

// ============================================================
// Parent-project aggregate stats
// ============================================================

// ParentProjectStats bundles the counts a parent overview surfaces:
// how many children the user can see, how many posts those children
// hold across all their seasons, how many distinct contributors, and
// how many of those children currently run an active season.
//
// These power the cockpit KPI tiles in Sprint 16e but are also used
// by the plain Sub-Projects section in 16d.
type ParentProjectStats struct {
	ChildCount         int
	ChildPostsCount    int
	ChildContributors  int
	ActiveChildSeasons int
}

// GetParentProjectStats runs four small aggregate queries (kept
// separate so each can use its own indexed path - one big CTE would
// hide which one is slow). The visibility filter mirrors the rest of
// the hierarchy methods so a viewer never sees counts they wouldn't
// see clicking through.
func (s *ProjectStore) GetParentProjectStats(ctx context.Context, parentID, viewerID int64) (*ParentProjectStats, error) {
	stats := &ParentProjectStats{}

	if err := s.DB.QueryRow(ctx,
		`SELECT COUNT(*)::int
		   FROM projects p
		  WHERE p.parent_project_id = $1
		    AND p.deleted_at IS NULL
		    AND (
		        p.visibility = 'public'
		        OR EXISTS (
		            SELECT 1 FROM project_members pm
		             WHERE pm.project_id = p.id AND pm.user_id = $2
		        )
		    )`, parentID, viewerID,
	).Scan(&stats.ChildCount); err != nil {
		return nil, fmt.Errorf("parent stats: child count: %w", err)
	}

	if err := s.DB.QueryRow(ctx,
		`SELECT COALESCE(COUNT(po.id), 0)::int
		   FROM projects p
		   JOIN seasons se ON se.project_id = p.id
		   JOIN posts po   ON po.season_id  = se.id
		  WHERE p.parent_project_id = $1
		    AND p.deleted_at IS NULL
		    AND (
		        p.visibility = 'public'
		        OR EXISTS (
		            SELECT 1 FROM project_members pm
		             WHERE pm.project_id = p.id AND pm.user_id = $2
		        )
		    )`, parentID, viewerID,
	).Scan(&stats.ChildPostsCount); err != nil {
		return nil, fmt.Errorf("parent stats: posts: %w", err)
	}

	if err := s.DB.QueryRow(ctx,
		`SELECT COALESCE(COUNT(DISTINCT po.author_id), 0)::int
		   FROM projects p
		   JOIN seasons se ON se.project_id = p.id
		   JOIN posts po   ON po.season_id  = se.id
		  WHERE p.parent_project_id = $1
		    AND p.deleted_at IS NULL
		    AND (
		        p.visibility = 'public'
		        OR EXISTS (
		            SELECT 1 FROM project_members pm
		             WHERE pm.project_id = p.id AND pm.user_id = $2
		        )
		    )`, parentID, viewerID,
	).Scan(&stats.ChildContributors); err != nil {
		return nil, fmt.Errorf("parent stats: contributors: %w", err)
	}

	if err := s.DB.QueryRow(ctx,
		`SELECT COUNT(DISTINCT p.id)::int
		   FROM projects p
		   JOIN seasons se ON se.project_id = p.id AND se.status = 'active'
		  WHERE p.parent_project_id = $1
		    AND p.deleted_at IS NULL
		    AND (
		        p.visibility = 'public'
		        OR EXISTS (
		            SELECT 1 FROM project_members pm
		             WHERE pm.project_id = p.id AND pm.user_id = $2
		        )
		    )`, parentID, viewerID,
	).Scan(&stats.ActiveChildSeasons); err != nil {
		return nil, fmt.Errorf("parent stats: active seasons: %w", err)
	}

	return stats, nil
}

// PostCountsByParentLast90Days returns weekly post-count buckets per
// child project of `parentID` for the last 90 days. Powers the
// stacked-area Activity chart in the Sprint 16e cockpit.
//
// Schema is identical to PostCountsBySpaceLast90Days so the chart-
// builder can be shared. The visibility filter only includes children
// the viewer can see, matching ListChildProjects.
func (s *ProjectStore) PostCountsByParentLast90Days(ctx context.Context, parentID, viewerID int64) ([]WeeklyProjectCount, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT date_trunc('week', po.created_at)::date AS week_start,
		       pr.id AS project_id,
		       COUNT(*) AS post_count
		  FROM posts po
		  JOIN seasons  se ON se.id = po.season_id
		  JOIN projects pr ON pr.id = se.project_id
		 WHERE pr.parent_project_id = $1
		   AND pr.deleted_at IS NULL
		   AND po.created_at >= NOW() - INTERVAL '90 days'
		   AND (
		       pr.visibility = 'public'
		       OR EXISTS (
		           SELECT 1 FROM project_members pm
		            WHERE pm.project_id = pr.id AND pm.user_id = $2
		       )
		   )
		 GROUP BY week_start, pr.id
		 ORDER BY week_start`,
		parentID, viewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("post counts by parent last 90: %w", err)
	}
	defer rows.Close()

	var out []WeeklyProjectCount
	for rows.Next() {
		var w WeeklyProjectCount
		if err := rows.Scan(&w.WeekStart, &w.ProjectID, &w.Count); err != nil {
			return nil, fmt.Errorf("scan parent weekly count: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
