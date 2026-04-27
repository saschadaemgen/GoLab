package model

import (
	"context"
	"fmt"
	"time"
)

// Sprint 16b visual-polish layer: aggregates that power the Space
// overview, project landing dashboard, and season detail dashboard.
// Each method runs a single query (or one per panel) so per-panel
// page loads stay under the 500ms budget the addendum sets even on
// projects with thousands of posts.
//
// Indexes that backstop these queries (all from migration 029):
//   posts.season_id        idx_posts_season (partial)
//   seasons.project_id     idx_seasons_project_status, idx_seasons_project_number
//   project_docs.project_id idx_project_docs_project
//   projects.space_id      idx_projects_space_slug (partial unique)

// ============================================================
// Space overview
// ============================================================

// ProjectWithStats is the row shape ListBySpaceWithStats returns. The
// embedded Project gives templates direct access to .Slug, .Name etc.
// without an extra wrapper field.
type ProjectWithStats struct {
	Project
	PostCount              int64
	LastActivityAt         *time.Time
	CurrentSeasonNumber    *int
	CurrentSeasonStartedAt *time.Time
	CurrentSeasonStatus    string
	TotalSeasonCount       int
	MemberCount            int
}

// SeasonProgress turns the project's status + current-season state
// into the 0-5 dot value the compact card renders. Pure function so
// templates and tests can call it identically.
func (p *ProjectWithStats) SeasonProgress() int {
	switch p.Status {
	case ProjectStatusDraft:
		return 0
	case ProjectStatusArchived:
		return 4
	case ProjectStatusClosed:
		return 5
	}
	if p.CurrentSeasonNumber == nil {
		return 0
	}
	switch p.CurrentSeasonStatus {
	case SeasonStatusPlanned:
		return 1
	case SeasonStatusClosed:
		return 5
	case SeasonStatusActive:
		if p.CurrentSeasonStartedAt == nil {
			return 2
		}
		days := time.Since(*p.CurrentSeasonStartedAt).Hours() / 24
		switch {
		case days < 7:
			return 2
		case days < 30:
			return 3
		default:
			return 4
		}
	}
	return 0
}

// ListBySpaceWithStats returns every visible project in the space
// with PostCount, LastActivityAt, current-season summary, total
// season count, and member count - all in a single query. The
// visibility filter mirrors ListBySpace.
func (s *ProjectStore) ListBySpaceWithStats(ctx context.Context, spaceID, viewerID int64) ([]ProjectWithStats, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT
			p.id, p.space_id, p.slug, p.name, p.description, p.status, p.visibility,
			p.owner_id, p.icon, p.color, p.parent_project_id,
			p.deleted_at, p.created_at, p.updated_at,
			COALESCE(s.slug, ''), COALESCE(s.name, ''),
			COALESCE(parent.slug, ''), COALESCE(parent.name, ''),
			COALESCE(stats.post_count, 0)::bigint,
			stats.last_activity_at,
			cur.season_number,
			cur.started_at,
			COALESCE(cur.status, '')::text,
			COALESCE(season_count.total, 0)::int,
			COALESCE(member_count.total, 0)::int
		FROM projects p
		LEFT JOIN spaces s ON p.space_id = s.id
		LEFT JOIN projects parent ON parent.id = p.parent_project_id
		LEFT JOIN (
			SELECT pr.id,
			       COUNT(po.id) AS post_count,
			       MAX(po.created_at) AS last_activity_at
			  FROM projects pr
			  LEFT JOIN seasons se ON se.project_id = pr.id
			  LEFT JOIN posts po   ON po.season_id  = se.id
			 WHERE pr.space_id = $1 AND pr.deleted_at IS NULL
			 GROUP BY pr.id
		) stats ON stats.id = p.id
		LEFT JOIN LATERAL (
			SELECT season_number, started_at, status
			  FROM seasons
			 WHERE project_id = p.id AND status = 'active'
			 ORDER BY season_number DESC
			 LIMIT 1
		) cur ON true
		LEFT JOIN (
			SELECT project_id, COUNT(*) AS total
			  FROM seasons GROUP BY project_id
		) season_count ON season_count.project_id = p.id
		LEFT JOIN (
			SELECT project_id, COUNT(*) AS total
			  FROM project_members GROUP BY project_id
		) member_count ON member_count.project_id = p.id
		WHERE p.space_id = $1
		  AND p.deleted_at IS NULL
		  AND (
		      p.visibility = 'public'
		      OR EXISTS (
		          SELECT 1 FROM project_members pm
		           WHERE pm.project_id = p.id AND pm.user_id = $2
		      )
		  )
		ORDER BY COALESCE(stats.last_activity_at, p.created_at) DESC`,
		spaceID, viewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects with stats: %w", err)
	}
	defer rows.Close()

	var out []ProjectWithStats
	for rows.Next() {
		var ps ProjectWithStats
		if err := rows.Scan(
			&ps.ID, &ps.SpaceID, &ps.Slug, &ps.Name, &ps.Description,
			&ps.Status, &ps.Visibility, &ps.OwnerID, &ps.Icon, &ps.Color,
			&ps.ParentProjectID,
			&ps.DeletedAt, &ps.CreatedAt, &ps.UpdatedAt,
			&ps.SpaceSlug, &ps.SpaceName,
			&ps.ParentSlug, &ps.ParentName,
			&ps.PostCount, &ps.LastActivityAt,
			&ps.CurrentSeasonNumber, &ps.CurrentSeasonStartedAt, &ps.CurrentSeasonStatus,
			&ps.TotalSeasonCount, &ps.MemberCount,
		); err != nil {
			return nil, fmt.Errorf("scan project with stats: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// WeeklyProjectCount is one row of the activity-bar-chart data.
// Each row is a (week start, project id, post count) triple. The
// chart-init JS pivots these into a stacked bar dataset.
type WeeklyProjectCount struct {
	WeekStart time.Time
	ProjectID int64
	Count     int
}

// PostCountsBySpaceLast90Days returns post counts bucketed by ISO
// week and project, scoped to one space, for the last 90 days. Used
// by the activity chart on the space overview.
func (s *ProjectStore) PostCountsBySpaceLast90Days(ctx context.Context, spaceID, viewerID int64) ([]WeeklyProjectCount, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT date_trunc('week', po.created_at)::date AS week_start,
		       pr.id AS project_id,
		       COUNT(*) AS post_count
		  FROM posts po
		  JOIN seasons  se ON se.id = po.season_id
		  JOIN projects pr ON pr.id = se.project_id
		 WHERE pr.space_id = $1
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
		spaceID, viewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("post counts by space last 90: %w", err)
	}
	defer rows.Close()

	var out []WeeklyProjectCount
	for rows.Next() {
		var w WeeklyProjectCount
		if err := rows.Scan(&w.WeekStart, &w.ProjectID, &w.Count); err != nil {
			return nil, fmt.Errorf("scan weekly count: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ============================================================
// Project landing dashboard
// ============================================================

// DailyCount is the (date, count) pair used by the heatmap and the
// season-detail line chart.
type DailyCount struct {
	Day   time.Time
	Count int
}

// SeasonPostCount is one row of the posts-per-season bar chart.
// Includes status so the renderer can highlight the active season.
type SeasonPostCount struct {
	SeasonID     int64
	SeasonNumber int
	Title        string
	Status       string
	PostCount    int
}

// ProjectStats bundles every aggregate the project-detail dashboard
// needs. GetProjectStats fills it in three round-trips (one per chart
// or per cluster) so large projects don't blow up a single query.
type ProjectStats struct {
	TotalPosts         int
	TotalContributors  int
	ActiveDays         int
	DocsCompleted      int
	PostCountsByDay    []DailyCount      // last 84 days for heatmap
	PostCountsBySeason []SeasonPostCount // ordered by season_number
}

// GetProjectStats runs the three aggregate queries the project
// dashboard needs. The page handler can call all three in parallel
// if it cares about latency; sequentially they typically come back
// in well under the 500ms budget at expected sizes.
func (s *ProjectStore) GetProjectStats(ctx context.Context, projectID int64) (*ProjectStats, error) {
	stats := &ProjectStats{}

	// Cluster 1: totals + docs in one query via subqueries. Each
	// subquery is over an indexed column so the planner picks index
	// scans even on large tables.
	if err := s.DB.QueryRow(ctx, `
		SELECT
		  COALESCE((
		    SELECT COUNT(po.id)
		      FROM posts po JOIN seasons se ON se.id = po.season_id
		     WHERE se.project_id = $1
		  ), 0)::int AS total_posts,
		  COALESCE((
		    SELECT COUNT(DISTINCT po.author_id)
		      FROM posts po JOIN seasons se ON se.id = po.season_id
		     WHERE se.project_id = $1
		  ), 0)::int AS total_contributors,
		  COALESCE((
		    SELECT COUNT(DISTINCT date_trunc('day', po.created_at)::date)
		      FROM posts po JOIN seasons se ON se.id = po.season_id
		     WHERE se.project_id = $1
		  ), 0)::int AS active_days,
		  COALESCE((
		    SELECT COUNT(*) FROM project_docs
		     WHERE project_id = $1
		       AND doc_type IN ('concept','architecture','workflow','roadmap')
		       AND content_html <> ''
		  ), 0)::int AS docs_completed`,
		projectID,
	).Scan(&stats.TotalPosts, &stats.TotalContributors,
		&stats.ActiveDays, &stats.DocsCompleted); err != nil {
		return nil, fmt.Errorf("project stats totals: %w", err)
	}

	// Cluster 2: post counts per day for the last 84 days (12 weeks
	// × 7 days = the heatmap grid).
	rows, err := s.DB.Query(ctx, `
		SELECT date_trunc('day', po.created_at)::date AS day, COUNT(*)::int
		  FROM posts po
		  JOIN seasons se ON se.id = po.season_id
		 WHERE se.project_id = $1
		   AND po.created_at >= CURRENT_DATE - INTERVAL '84 days'
		 GROUP BY day
		 ORDER BY day`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("project stats daily: %w", err)
	}
	for rows.Next() {
		var d DailyCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan project daily: %w", err)
		}
		stats.PostCountsByDay = append(stats.PostCountsByDay, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cluster 3: post counts per season for the bar chart.
	rows, err = s.DB.Query(ctx, `
		SELECT se.id, se.season_number, se.title, se.status,
		       COALESCE(COUNT(po.id), 0)::int AS post_count
		  FROM seasons se
		  LEFT JOIN posts po ON po.season_id = se.id
		 WHERE se.project_id = $1
		 GROUP BY se.id, se.season_number, se.title, se.status
		 ORDER BY se.season_number`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("project stats per season: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sp SeasonPostCount
		if err := rows.Scan(&sp.SeasonID, &sp.SeasonNumber, &sp.Title,
			&sp.Status, &sp.PostCount); err != nil {
			return nil, fmt.Errorf("scan project per-season: %w", err)
		}
		stats.PostCountsBySeason = append(stats.PostCountsBySeason, sp)
	}
	return stats, rows.Err()
}

// ============================================================
// Season detail dashboard
// ============================================================

// SeasonStats is the four-KPI plus chart-data bundle for the season
// detail dashboard.
type SeasonStats struct {
	PostCount         int
	ContributorCount  int
	DaysRunning       int
	LinkedDocsCount   int
	PostCountsByDay   []DailyCount   // last 30 days for line chart
	PostCountsByType  map[string]int // for donut chart
}

// GetSeasonStats fills the dashboard in three queries (totals,
// per-day series, per-type counts).
func (s *SeasonStore) GetSeasonStats(ctx context.Context, seasonID int64) (*SeasonStats, error) {
	stats := &SeasonStats{
		PostCountsByType: map[string]int{},
	}

	// Totals + days running. days_running is computed from the
	// season's started_at to NOW() (or closed_at when closed).
	// Fallback to created_at if started_at is NULL (planned season
	// that someone is statting prematurely - days_running = 0).
	if err := s.DB.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT COUNT(*) FROM posts WHERE season_id = $1), 0)::int,
		  COALESCE((SELECT COUNT(DISTINCT author_id) FROM posts WHERE season_id = $1), 0)::int,
		  COALESCE((
		    SELECT GREATEST(0,
		      EXTRACT(DAY FROM
		        COALESCE(closed_at, NOW()) - COALESCE(started_at, NOW())
		      )::int
		    ) FROM seasons WHERE id = $1
		  ), 0)::int,
		  COALESCE((
		    SELECT COUNT(*) FROM project_docs pd
		      JOIN seasons se ON se.id = $1
		     WHERE pd.project_id = se.project_id
		  ), 0)::int`,
		seasonID,
	).Scan(&stats.PostCount, &stats.ContributorCount,
		&stats.DaysRunning, &stats.LinkedDocsCount); err != nil {
		return nil, fmt.Errorf("season stats totals: %w", err)
	}

	// Daily series for the last 30 days.
	rows, err := s.DB.Query(ctx, `
		SELECT date_trunc('day', created_at)::date, COUNT(*)::int
		  FROM posts
		 WHERE season_id = $1
		   AND created_at >= CURRENT_DATE - INTERVAL '30 days'
		 GROUP BY date_trunc('day', created_at)::date
		 ORDER BY 1`,
		seasonID,
	)
	if err != nil {
		return nil, fmt.Errorf("season stats daily: %w", err)
	}
	for rows.Next() {
		var d DailyCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan season daily: %w", err)
		}
		stats.PostCountsByDay = append(stats.PostCountsByDay, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Per-type donut data.
	rows, err = s.DB.Query(ctx, `
		SELECT post_type, COUNT(*)::int
		  FROM posts
		 WHERE season_id = $1
		 GROUP BY post_type`,
		seasonID,
	)
	if err != nil {
		return nil, fmt.Errorf("season stats per-type: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, fmt.Errorf("scan season per-type: %w", err)
		}
		stats.PostCountsByType[t] = n
	}
	return stats, rows.Err()
}

