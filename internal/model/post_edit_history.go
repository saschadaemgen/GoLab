package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostEditKind distinguishes author self-edits from admin
// moderation edits. Sprint 15a ships only the "author" path; the
// "admin" value is reserved for Sprint 15c.
const (
	PostEditKindAuthor = "author"
	PostEditKindAdmin  = "admin"
)

// PostEditHistory is a single row of migration 025's
// post_edit_history table. PreviousContent is what the post said
// BEFORE this edit; the current content lives on the posts row.
type PostEditHistory struct {
	ID              int64     `json:"id"`
	PostID          int64     `json:"post_id"`
	EditorID        int64     `json:"editor_id"`
	PreviousContent string    `json:"previous_content"`
	EditReason      string    `json:"edit_reason,omitempty"`
	EditKind        string    `json:"edit_kind"`
	CreatedAt       time.Time `json:"created_at"`
}

// PostEditHistoryStore wraps the post_edit_history table.
type PostEditHistoryStore struct {
	DB *pgxpool.Pool
}

// Record inserts a new history row. Callers should wrap this plus
// the posts UPDATE in a transaction so a failed update doesn't
// leave a history row claiming a change that never happened.
// Sprint 15a's PostStore.UpdateContent does exactly that; callers
// outside that method should follow the same pattern.
func (s *PostEditHistoryStore) Record(ctx context.Context, tx pgx.Tx, h PostEditHistory) (int64, error) {
	if h.EditKind == "" {
		h.EditKind = PostEditKindAuthor
	}
	var id int64
	err := tx.QueryRow(ctx,
		`INSERT INTO post_edit_history
		     (post_id, editor_id, previous_content, edit_reason, edit_kind)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		h.PostID, h.EditorID, h.PreviousContent, h.EditReason, h.EditKind,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert edit history: %w", err)
	}
	return id, nil
}

// LastEditAt returns the created_at of the most recent edit for
// this post, or the zero time if no edits exist. Used by the
// post-card template to decide whether to render the "edited"
// badge next to the timestamp.
//
// Sprint 15a B8 Nit 3: the previous implementation scanned SELECT
// MAX(...) into a bare time.Time. MAX() on an empty set returns one
// row whose column is NULL (not zero rows), so ErrNoRows never
// fired; pgx's decoder tried to decode NULL into a non-nullable
// time.Time and returned a generic scan error instead. Callers
// then saw a "last edit at: scanning ..." error instead of the
// documented zero-time sentinel. Scanning into *time.Time makes
// pgx leave the destination nil on NULL, which we then translate
// to the documented contract.
func (s *PostEditHistoryStore) LastEditAt(ctx context.Context, postID int64) (time.Time, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx,
		`SELECT MAX(created_at) FROM post_edit_history WHERE post_id = $1`,
		postID,
	).Scan(&t)
	if err == pgx.ErrNoRows {
		// Defensive: an aggregate over an empty set normally still
		// returns one row with NULL. Kept for symmetry with other
		// stores in this file.
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("last edit at: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// AttachEditedAt is the feed-friendly batch version of LastEditAt.
// One aggregation query across all post ids in the slice, mutates
// Post.EditedAt in place for posts that have at least one history
// row. Posts with no edits are left alone. Callers usually invoke
// this right after ReactionStore.AttachTo so the post-card template
// sees both enrichments in a single render pass.
func (s *PostEditHistoryStore) AttachEditedAt(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]int64, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	rows, err := s.DB.Query(ctx,
		`SELECT post_id, MAX(created_at)
		   FROM post_edit_history
		  WHERE post_id = ANY($1)
		  GROUP BY post_id`,
		ids,
	)
	if err != nil {
		return fmt.Errorf("attach edited_at: %w", err)
	}
	defer rows.Close()
	m := make(map[int64]time.Time, len(posts))
	for rows.Next() {
		var pid int64
		var t time.Time
		if err := rows.Scan(&pid, &t); err != nil {
			return fmt.Errorf("scan edited_at: %w", err)
		}
		m[pid] = t
	}
	for i := range posts {
		if t, ok := m[posts[i].ID]; ok && !t.IsZero() {
			copied := t
			posts[i].EditedAt = &copied
		}
	}
	return nil
}

// ListForPost returns every edit for a post, newest first. Bounded
// so a malicious editor can't tank the query with ten thousand
// micro-edits.
func (s *PostEditHistoryStore) ListForPost(ctx context.Context, postID int64, limit int) ([]PostEditHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx,
		`SELECT id, post_id, editor_id, previous_content, edit_reason, edit_kind, created_at
		   FROM post_edit_history
		  WHERE post_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		postID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list edit history: %w", err)
	}
	defer rows.Close()
	var out []PostEditHistory
	for rows.Next() {
		var h PostEditHistory
		var reason *string
		if err := rows.Scan(&h.ID, &h.PostID, &h.EditorID, &h.PreviousContent, &reason, &h.EditKind, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan edit history: %w", err)
		}
		if reason != nil {
			h.EditReason = *reason
		}
		out = append(out, h)
	}
	return out, nil
}
