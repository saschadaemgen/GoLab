package model

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Tag is a lightweight per-post marker. Tags are case-folded and
// slugified so "Docker" and "docker" collapse into the same tag row.
type Tag struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	UseCount  int64     `json:"use_count"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TagStore struct {
	DB *pgxpool.Pool
}

// slugify strips down a human name to a URL-safe slug:
//   - lowercase
//   - spaces -> hyphens
//   - drops anything outside [a-z0-9-]
//   - collapses multiple hyphens
//   - trims leading/trailing hyphens
//
// Matches the client-side sanitizer in golab.js tagInput() so the
// server never disagrees with what the user typed into the compose box.
var tagCleanRe = regexp.MustCompile(`[^a-z0-9-]+`)
var tagDashesRe = regexp.MustCompile(`-+`)

func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = tagCleanRe.ReplaceAllString(s, "")
	s = tagDashesRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

// FindOrCreate returns the existing tag by slug or creates it.
// The createdBy field is only set on the insert path.
func (t *TagStore) FindOrCreate(ctx context.Context, name string, createdBy int64) (*Tag, error) {
	slug := Slugify(name)
	if slug == "" {
		return nil, fmt.Errorf("invalid tag name")
	}

	// Try insert first. ON CONFLICT DO NOTHING so a race between two
	// users creating the same tag doesn't error.
	_, err := t.DB.Exec(ctx, `
		INSERT INTO tags (name, slug, created_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (slug) DO NOTHING`, slug, slug, createdBy)
	if err != nil {
		return nil, fmt.Errorf("creating tag: %w", err)
	}

	tag := &Tag{}
	err = t.DB.QueryRow(ctx, `
		SELECT id, name, slug, use_count, created_by, created_at
		FROM tags WHERE slug = $1`, slug).Scan(
		&tag.ID, &tag.Name, &tag.Slug, &tag.UseCount, &tag.CreatedBy, &tag.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("reading tag: %w", err)
	}
	return tag, nil
}

// Search powers the compose-box autocomplete. Slug prefix match, then
// popularity.
func (t *TagStore) Search(ctx context.Context, query string, limit int) ([]Tag, error) {
	prefix := Slugify(query)
	if prefix == "" {
		return t.ListPopular(ctx, limit)
	}
	rows, err := t.DB.Query(ctx, `
		SELECT id, name, slug, use_count, created_by, created_at
		FROM tags
		WHERE slug LIKE $1 || '%'
		ORDER BY use_count DESC, slug ASC
		LIMIT $2`, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("searching tags: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

// ListByPost returns the tags attached to a specific post, ordered by
// use_count so the most "central" tag shows first.
func (t *TagStore) ListByPost(ctx context.Context, postID int64) ([]Tag, error) {
	rows, err := t.DB.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.use_count, t.created_by, t.created_at
		FROM tags t
		JOIN post_tags pt ON pt.tag_id = t.id
		WHERE pt.post_id = $1
		ORDER BY t.use_count DESC, t.slug ASC`, postID)
	if err != nil {
		return nil, fmt.Errorf("listing tags for post: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

// ListPopular returns the globally most-used tags. Used as the default
// when the user opens the tag autocomplete with an empty query.
func (t *TagStore) ListPopular(ctx context.Context, limit int) ([]Tag, error) {
	rows, err := t.DB.Query(ctx, `
		SELECT id, name, slug, use_count, created_by, created_at
		FROM tags
		WHERE use_count > 0
		ORDER BY use_count DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing popular tags: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

// ListBySpace returns the most-used tags within one space. Shown in
// the space page sidebar "Popular tags" panel.
func (t *TagStore) ListBySpace(ctx context.Context, spaceID int64, limit int) ([]Tag, error) {
	rows, err := t.DB.Query(ctx, `
		SELECT t.id, t.name, t.slug, COUNT(*) AS cnt, t.created_by, t.created_at
		FROM tags t
		JOIN post_tags pt ON pt.tag_id = t.id
		JOIN posts p ON p.id = pt.post_id
		WHERE p.space_id = $1
		GROUP BY t.id, t.name, t.slug, t.created_by, t.created_at
		ORDER BY cnt DESC
		LIMIT $2`, spaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing tags by space: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

// AttachToPost inserts the (post, tag) join rows and bumps use_count
// by 1 for each new attachment. Duplicates are ignored.
func (t *TagStore) AttachToPost(ctx context.Context, postID int64, tagIDs []int64) error {
	if len(tagIDs) == 0 {
		return nil
	}
	for _, tagID := range tagIDs {
		// Individual inserts so we can cleanly detect which ones were new
		// (RowsAffected==1) and increment use_count only for those.
		tag, err := t.DB.Exec(ctx, `
			INSERT INTO post_tags (post_id, tag_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, postID, tagID)
		if err != nil {
			return fmt.Errorf("attaching tag %d: %w", tagID, err)
		}
		if tag.RowsAffected() == 1 {
			if _, err := t.DB.Exec(ctx,
				`UPDATE tags SET use_count = use_count + 1 WHERE id = $1`, tagID); err != nil {
				return fmt.Errorf("bumping tag use_count: %w", err)
			}
		}
	}
	return nil
}

// FindBySlug is used by the tag page handler.
func (t *TagStore) FindBySlug(ctx context.Context, slug string) (*Tag, error) {
	tag := &Tag{}
	err := t.DB.QueryRow(ctx, `
		SELECT id, name, slug, use_count, created_by, created_at
		FROM tags WHERE slug = $1`, slug).Scan(
		&tag.ID, &tag.Name, &tag.Slug, &tag.UseCount, &tag.CreatedBy, &tag.CreatedAt,
	)
	if err != nil {
		return nil, nil // not found is not an error for the caller
	}
	return tag, nil
}

// scanTags centralizes row scanning so every list method uses the same
// column order.
func scanTags(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]Tag, error) {
	var out []Tag
	for rows.Next() {
		var tg Tag
		if err := rows.Scan(&tg.ID, &tg.Name, &tg.Slug, &tg.UseCount, &tg.CreatedBy, &tg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		out = append(out, tg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tags: %w", err)
	}
	return out, nil
}
