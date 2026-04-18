package model

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReactionTypesOrdered is the canonical display order of the 6
// emoji types. Kept as the single source of truth for both the
// template chip bar and the StateFor zero-init loop, so a new
// emoji type added in the future only touches this slice.
var ReactionTypesOrdered = []string{
	"heart",
	"thumbsup",
	"laugh",
	"surprised",
	"sad",
	"fire",
}

// ReactionEmoji maps each allowed type to its rendered glyph.
// Exposed so template helpers can look up the emoji for a type
// without hard-coding the table in HTML.
var ReactionEmoji = map[string]string{
	"heart":     "\u2764\ufe0f", // red heart
	"thumbsup":  "\U0001f44d",   // thumbs up
	"laugh":     "\U0001f602",   // face with tears of joy
	"surprised": "\U0001f62e",   // face with open mouth
	"sad":       "\U0001f622",   // crying face
	"fire":      "\U0001f525",   // fire
}

// IsValidReactionType returns true iff t is in the allowlist.
// Shared between the handler's input validation and template
// rendering.
func IsValidReactionType(t string) bool {
	_, ok := ReactionEmoji[t]
	return ok
}

// ReactResult is returned by Toggle. Sprint 14 reduced the state
// machine from three (added/switched/removed) to two (added/
// removed) because a user can now hold multiple distinct emoji
// types on a single post - switching is two operations, not one.
type ReactResult string

const (
	ReactAdded   ReactResult = "added"
	ReactRemoved ReactResult = "removed"
)

// ReactionState is the per-post snapshot the client needs to
// render all 6 chips with counts and the current user's own
// reaction set. Counts always contains every type in
// ReactionTypesOrdered (zero when none), so templates never need
// nil checks.
type ReactionState struct {
	Counts    map[string]int `json:"counts"`
	UserTypes []string       `json:"user_types"`
}

// emptyState returns a zero-initialised ReactionState with every
// known emoji type present at 0. Used as the neutral starting
// point for merging DB rows into.
func emptyState() ReactionState {
	counts := make(map[string]int, len(ReactionTypesOrdered))
	for _, t := range ReactionTypesOrdered {
		counts[t] = 0
	}
	return ReactionState{Counts: counts, UserTypes: []string{}}
}

type ReactionStore struct {
	DB *pgxpool.Pool
}

// Toggle applies the GitHub-style multi-emoji semantics: if the
// exact (user, post, type) triple exists, remove that row; else
// insert it. A user holding "heart" who clicks "thumbsup" gains a
// second reaction - the UI asks for that as a separate Toggle
// call. After either branch, posts.reaction_count is recomputed
// from the table as the total distinct-row count for the post.
func (s *ReactionStore) Toggle(ctx context.Context, userID, postID int64, reactionType string) (ReactResult, error) {
	if !IsValidReactionType(reactionType) {
		reactionType = "heart"
	}

	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM reactions
		    WHERE user_id = $1 AND post_id = $2 AND reaction_type = $3
		 )`,
		userID, postID, reactionType,
	).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("checking reaction: %w", err)
	}

	var result ReactResult
	if exists {
		if _, err := s.DB.Exec(ctx,
			`DELETE FROM reactions
			 WHERE user_id = $1 AND post_id = $2 AND reaction_type = $3`,
			userID, postID, reactionType,
		); err != nil {
			return "", fmt.Errorf("removing reaction: %w", err)
		}
		result = ReactRemoved
	} else {
		if _, err := s.DB.Exec(ctx,
			`INSERT INTO reactions (user_id, post_id, reaction_type)
			 VALUES ($1, $2, $3)`,
			userID, postID, reactionType,
		); err != nil {
			return "", fmt.Errorf("inserting reaction: %w", err)
		}
		result = ReactAdded
	}

	// Recompute cached count. Counts EVERY row for the post across
	// all emoji types - a user with two reactions on the same post
	// contributes 2 to the total.
	if _, err := s.DB.Exec(ctx,
		`UPDATE posts
		 SET reaction_count = (SELECT COUNT(*) FROM reactions WHERE post_id = $1),
		     updated_at = NOW()
		 WHERE id = $1`,
		postID,
	); err != nil {
		return result, fmt.Errorf("updating reaction count: %w", err)
	}
	return result, nil
}

// React is a legacy shim that maps to Toggle. Kept so existing
// callers (and any unconverted tests) don't break in a single
// commit; remove in Sprint 15 after the client-side rollout is
// stable.
func (s *ReactionStore) React(ctx context.Context, userID, postID int64, reactionType string) error {
	_, err := s.Toggle(ctx, userID, postID, reactionType)
	return err
}

// Unreact wipes EVERY reaction the user has on the post (all
// emoji types). The new UI never calls this - it's a fallback
// for old clients predating Sprint 14. PostHandler.Unreact logs
// at Warn level when this endpoint is hit so we can track
// remaining legacy callers.
func (s *ReactionStore) Unreact(ctx context.Context, userID, postID int64) error {
	if _, err := s.DB.Exec(ctx,
		`DELETE FROM reactions WHERE user_id = $1 AND post_id = $2`,
		userID, postID,
	); err != nil {
		return fmt.Errorf("unreacting: %w", err)
	}
	if _, err := s.DB.Exec(ctx,
		`UPDATE posts
		 SET reaction_count = (SELECT COUNT(*) FROM reactions WHERE post_id = $1),
		     updated_at = NOW()
		 WHERE id = $1`,
		postID,
	); err != nil {
		return fmt.Errorf("updating reaction count: %w", err)
	}
	return nil
}

// HasReacted is kept for callsites that only need a boolean.
// With multi-reaction semantics "has reacted at all" is true iff
// the user holds at least one emoji type on the post.
func (s *ReactionStore) HasReacted(ctx context.Context, userID, postID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM reactions WHERE user_id = $1 AND post_id = $2)`,
		userID, postID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking reaction: %w", err)
	}
	return exists, nil
}

// UserReactionTypes returns every emoji type the user has placed
// on the post, in no particular order. Replaces the Sprint 10.5
// UserReactionType which returned a single string.
func (s *ReactionStore) UserReactionTypes(ctx context.Context, userID, postID int64) ([]string, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT reaction_type FROM reactions WHERE user_id = $1 AND post_id = $2`,
		userID, postID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing user reaction types: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scan user reaction: %w", err)
		}
		out = append(out, t)
	}
	return out, nil
}

// StateFor fetches the full reaction snapshot for a single post.
// Two queries, one transaction-less pair: the aggregate counts,
// then the current user's own reactions. Callers that need many
// posts should use StateForMany instead to avoid N+1.
func (s *ReactionStore) StateFor(ctx context.Context, userID, postID int64) (ReactionState, error) {
	state := emptyState()

	rows, err := s.DB.Query(ctx,
		`SELECT reaction_type, COUNT(*)
		   FROM reactions
		  WHERE post_id = $1
		  GROUP BY reaction_type`,
		postID,
	)
	if err != nil {
		return state, fmt.Errorf("state counts: %w", err)
	}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			rows.Close()
			return state, fmt.Errorf("scan state count: %w", err)
		}
		// Only record types we know about - an out-of-allowlist row
		// (shouldn't happen, but defensively) is ignored rather than
		// exposed to the client.
		if _, ok := state.Counts[t]; ok {
			state.Counts[t] = n
		}
	}
	rows.Close()

	if userID > 0 {
		urows, err := s.DB.Query(ctx,
			`SELECT reaction_type
			   FROM reactions
			  WHERE post_id = $1 AND user_id = $2`,
			postID, userID,
		)
		if err != nil {
			return state, fmt.Errorf("state user types: %w", err)
		}
		for urows.Next() {
			var t string
			if err := urows.Scan(&t); err != nil {
				urows.Close()
				return state, fmt.Errorf("scan user type: %w", err)
			}
			state.UserTypes = append(state.UserTypes, t)
		}
		urows.Close()
	}

	return state, nil
}

// StateForMany batch-loads ReactionState for a slice of post IDs
// so feed rendering stays O(2) round-trips regardless of page
// size. Every input post id appears in the output map, even ones
// with no reactions (they map to an empty-counts state).
func (s *ReactionStore) StateForMany(ctx context.Context, userID int64, postIDs []int64) (map[int64]ReactionState, error) {
	out := make(map[int64]ReactionState, len(postIDs))
	for _, pid := range postIDs {
		out[pid] = emptyState()
	}
	if len(postIDs) == 0 {
		return out, nil
	}

	rows, err := s.DB.Query(ctx,
		`SELECT post_id, reaction_type, COUNT(*)
		   FROM reactions
		  WHERE post_id = ANY($1)
		  GROUP BY post_id, reaction_type`,
		postIDs,
	)
	if err != nil {
		return out, fmt.Errorf("batch counts: %w", err)
	}
	for rows.Next() {
		var pid int64
		var t string
		var n int
		if err := rows.Scan(&pid, &t, &n); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan batch count: %w", err)
		}
		if st, ok := out[pid]; ok {
			if _, known := st.Counts[t]; known {
				st.Counts[t] = n
				out[pid] = st
			}
		}
	}
	rows.Close()

	if userID > 0 {
		urows, err := s.DB.Query(ctx,
			`SELECT post_id, reaction_type
			   FROM reactions
			  WHERE post_id = ANY($1) AND user_id = $2`,
			postIDs, userID,
		)
		if err != nil {
			return out, fmt.Errorf("batch user types: %w", err)
		}
		for urows.Next() {
			var pid int64
			var t string
			if err := urows.Scan(&pid, &t); err != nil {
				urows.Close()
				return out, fmt.Errorf("scan batch user type: %w", err)
			}
			if st, ok := out[pid]; ok {
				st.UserTypes = append(st.UserTypes, t)
				out[pid] = st
			}
		}
		urows.Close()
	}

	return out, nil
}

// AttachTo enriches every Post in the slice with its ReactionCounts
// and UserReactionTypes fields. One StateForMany call regardless of
// slice length, so feed / thread / profile pages stay O(2) queries
// for reaction state. Modifies posts in place.
func (s *ReactionStore) AttachTo(ctx context.Context, userID int64, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]int64, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	states, err := s.StateForMany(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range posts {
		if st, ok := states[posts[i].ID]; ok {
			posts[i].ReactionCounts = st.Counts
			posts[i].UserReactionTypes = st.UserTypes
		}
	}
	return nil
}

// UserReactionType is the pre-Sprint-14 single-type getter. The
// one remaining caller is the legacy React JSON response shape
// in PostHandler. New code uses UserReactionTypes or StateFor.
// Kept as a shim that returns "" when the user has no reactions
// and the first type (arbitrary order) otherwise.
//
// Deprecated: use UserReactionTypes.
func (s *ReactionStore) UserReactionType(ctx context.Context, userID, postID int64) (string, error) {
	var t string
	err := s.DB.QueryRow(ctx,
		`SELECT reaction_type FROM reactions WHERE user_id = $1 AND post_id = $2 LIMIT 1`,
		userID, postID,
	).Scan(&t)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading user reaction type: %w", err)
	}
	return t, nil
}
