package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Space is a top-level topic area. Users navigate by Space first, then
// optionally filter by post type and tag. Channels (legacy) now nest
// inside Spaces.
type Space struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`

	// Computed at list time, not persisted.
	PostCount int64 `json:"post_count"`
}

type SpaceStore struct {
	DB *pgxpool.Pool
}

// List returns every space ordered by sort_order, each with a live
// post_count. Safe to call on every page render: the sub-query is
// indexed on posts(space_id) from migration 016.
func (s *SpaceStore) List(ctx context.Context) ([]Space, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, slug, description, icon, color, sort_order, created_at,
		       COALESCE((SELECT COUNT(*) FROM posts WHERE space_id = spaces.id
		                 AND parent_id IS NULL), 0) AS post_count
		FROM spaces
		ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing spaces: %w", err)
	}
	defer rows.Close()

	var out []Space
	for rows.Next() {
		var sp Space
		if err := rows.Scan(
			&sp.ID, &sp.Name, &sp.Slug, &sp.Description, &sp.Icon,
			&sp.Color, &sp.SortOrder, &sp.CreatedAt, &sp.PostCount,
		); err != nil {
			return nil, fmt.Errorf("scanning space: %w", err)
		}
		out = append(out, sp)
	}
	return out, nil
}

// FindBySlug returns the space with that slug or nil if not found.
func (s *SpaceStore) FindBySlug(ctx context.Context, slug string) (*Space, error) {
	sp := &Space{}
	err := s.DB.QueryRow(ctx, `
		SELECT id, name, slug, description, icon, color, sort_order, created_at,
		       COALESCE((SELECT COUNT(*) FROM posts WHERE space_id = spaces.id
		                 AND parent_id IS NULL), 0) AS post_count
		FROM spaces WHERE slug = $1`, slug).Scan(
		&sp.ID, &sp.Name, &sp.Slug, &sp.Description, &sp.Icon,
		&sp.Color, &sp.SortOrder, &sp.CreatedAt, &sp.PostCount,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding space by slug: %w", err)
	}
	return sp, nil
}

// FindByID is used when a post carries a space_id but not the joined
// name/slug (e.g. background jobs).
func (s *SpaceStore) FindByID(ctx context.Context, id int64) (*Space, error) {
	sp := &Space{}
	err := s.DB.QueryRow(ctx, `
		SELECT id, name, slug, description, icon, color, sort_order, created_at, 0
		FROM spaces WHERE id = $1`, id).Scan(
		&sp.ID, &sp.Name, &sp.Slug, &sp.Description, &sp.Icon,
		&sp.Color, &sp.SortOrder, &sp.CreatedAt, &sp.PostCount,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding space by id: %w", err)
	}
	return sp, nil
}
