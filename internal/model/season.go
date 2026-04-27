package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Season status values. Newly-created seasons sit in 'planned' until
// the owner runs Activate; once active, posts assigned to the season
// accumulate; Close transitions to 'closed' and stamps the closing
// document.
const (
	SeasonStatusPlanned = "planned"
	SeasonStatusActive  = "active"
	SeasonStatusClosed  = "closed"
)

// Season is one numbered iteration of work inside a project. The
// season_number is per-project sequential (1, 2, 3, ...); the unique
// index on (project_id, season_number) plus the MAX()+1 transaction
// in Create keeps the sequence dense even under concurrent inserts.
type Season struct {
	ID             int64      `json:"id"`
	ProjectID      int64      `json:"project_id"`
	SeasonNumber   int        `json:"season_number"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	ClosingDocMD   string     `json:"closing_doc_md"`
	ClosingDocHTML string     `json:"closing_doc_html"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type SeasonStore struct {
	DB *pgxpool.Pool
}

var (
	ErrSeasonNotFound   = errors.New("season not found")
	ErrSeasonNotPlanned = errors.New("season is not in planned state")
	ErrSeasonNotActive  = errors.New("season is not in active state")
)

const seasonCols = `
	id, project_id, season_number, title, description, status,
	started_at, closed_at, closing_doc_md, closing_doc_html,
	created_at, updated_at`

// SeasonCreateParams covers the human-supplied fields. season_number
// is computed inside Create.
type SeasonCreateParams struct {
	ProjectID   int64
	Title       string
	Description string
}

// Create inserts a season with the next sequential season_number for
// that project. The MAX()+1 lookup and INSERT run in one transaction;
// the unique index on (project_id, season_number) ensures that two
// concurrent creates can't pick the same number - the loser retries
// at the database level (serialisation failure becomes a wrapped
// error returned to the caller).
func (s *SeasonStore) Create(ctx context.Context, p SeasonCreateParams) (*Season, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create season: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var nextNum int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(season_number), 0) + 1
		 FROM seasons WHERE project_id = $1`, p.ProjectID,
	).Scan(&nextNum); err != nil {
		return nil, fmt.Errorf("create season: next number: %w", err)
	}

	se := &Season{}
	err = tx.QueryRow(ctx, `
		INSERT INTO seasons
			(project_id, season_number, title, description, status)
		VALUES ($1, $2, $3, $4, 'planned')
		RETURNING `+seasonCols,
		p.ProjectID, nextNum, p.Title, p.Description,
	).Scan(
		&se.ID, &se.ProjectID, &se.SeasonNumber, &se.Title, &se.Description,
		&se.Status, &se.StartedAt, &se.ClosedAt, &se.ClosingDocMD,
		&se.ClosingDocHTML, &se.CreatedAt, &se.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create season: insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create season: commit: %w", err)
	}
	return se, nil
}

func (s *SeasonStore) FindByID(ctx context.Context, id int64) (*Season, error) {
	se := &Season{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+seasonCols+` FROM seasons WHERE id = $1`, id,
	).Scan(
		&se.ID, &se.ProjectID, &se.SeasonNumber, &se.Title, &se.Description,
		&se.Status, &se.StartedAt, &se.ClosedAt, &se.ClosingDocMD,
		&se.ClosingDocHTML, &se.CreatedAt, &se.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find season by id: %w", err)
	}
	return se, nil
}

func (s *SeasonStore) GetByNumber(ctx context.Context, projectID int64, num int) (*Season, error) {
	se := &Season{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+seasonCols+` FROM seasons
		 WHERE project_id = $1 AND season_number = $2`,
		projectID, num,
	).Scan(
		&se.ID, &se.ProjectID, &se.SeasonNumber, &se.Title, &se.Description,
		&se.Status, &se.StartedAt, &se.ClosedAt, &se.ClosingDocMD,
		&se.ClosingDocHTML, &se.CreatedAt, &se.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get season by number: %w", err)
	}
	return se, nil
}

func (s *SeasonStore) ListByProject(ctx context.Context, projectID int64) ([]Season, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+seasonCols+` FROM seasons
		 WHERE project_id = $1
		 ORDER BY season_number`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list seasons: %w", err)
	}
	defer rows.Close()

	var out []Season
	for rows.Next() {
		var se Season
		if err := rows.Scan(
			&se.ID, &se.ProjectID, &se.SeasonNumber, &se.Title, &se.Description,
			&se.Status, &se.StartedAt, &se.ClosedAt, &se.ClosingDocMD,
			&se.ClosingDocHTML, &se.CreatedAt, &se.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan season: %w", err)
		}
		out = append(out, se)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seasons: %w", err)
	}
	return out, nil
}

// UpdateMeta updates only title + description. Status changes go
// through Activate/Close so the timestamps and closing doc stay
// consistent. season_number is intentionally immutable - renumbering
// would break the post.season_id references.
func (s *SeasonStore) UpdateMeta(ctx context.Context, id int64, title, description string) error {
	res, err := s.DB.Exec(ctx, `
		UPDATE seasons
		   SET title = $2, description = $3, updated_at = NOW()
		 WHERE id = $1`,
		id, title, description,
	)
	if err != nil {
		return fmt.Errorf("update season meta: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrSeasonNotFound
	}
	return nil
}

// Activate transitions a planned season to active and stamps
// started_at. Returns ErrSeasonNotFound when the row doesn't exist or
// ErrSeasonNotPlanned when the row is already active or closed.
func (s *SeasonStore) Activate(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `
		UPDATE seasons
		   SET status = 'active', started_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND status = 'planned'`, id,
	)
	if err != nil {
		return fmt.Errorf("activate season: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// Distinguish "row missing" from "row exists but wrong state" so
	// the handler can return 404 vs 409.
	var exists bool
	if err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM seasons WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return fmt.Errorf("activate season: post-check: %w", err)
	}
	if !exists {
		return ErrSeasonNotFound
	}
	return ErrSeasonNotPlanned
}

// Close transitions an active season to closed and stamps closed_at +
// the closing document. closingDocMD is the raw markdown the user
// submitted; closingDocHTML is the sanitised HTML produced by the
// handler via render.Markdown + render.Sanitizer.
func (s *SeasonStore) Close(ctx context.Context, id int64, closingDocMD, closingDocHTML string) error {
	tag, err := s.DB.Exec(ctx, `
		UPDATE seasons
		   SET status = 'closed',
		       closed_at = NOW(),
		       closing_doc_md = $2,
		       closing_doc_html = $3,
		       updated_at = NOW()
		 WHERE id = $1 AND status = 'active'`,
		id, closingDocMD, closingDocHTML,
	)
	if err != nil {
		return fmt.Errorf("close season: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM seasons WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return fmt.Errorf("close season: post-check: %w", err)
	}
	if !exists {
		return ErrSeasonNotFound
	}
	return ErrSeasonNotActive
}
