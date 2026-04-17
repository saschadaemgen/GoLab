package model

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReactionStore struct {
	DB *pgxpool.Pool
}

// ReactResult reports what happened so callers can render the right
// toast and update the UI.
type ReactResult string

const (
	ReactAdded    ReactResult = "added"    // user had no reaction, now has one
	ReactSwitched ReactResult = "switched" // user changed their reaction type
	ReactRemoved  ReactResult = "removed"  // user clicked their current type again, toggled off
)

// Toggle is Sprint 10.5 reaction logic. The existing PRIMARY KEY on
// (user_id, post_id) means each user has at most one reaction per
// post. Rather than drop the PK (forbidden by the "never drop" rule),
// we carry the reaction type inside that one row.
//
//   - If the user has already reacted with the same type, delete the row
//     (toggle off).
//   - If the user has reacted with a different type, UPDATE the type
//     (switch emoji).
//   - If the user has no row yet, INSERT.
//
// Callers get back which of those branches fired.
func (s *ReactionStore) Toggle(ctx context.Context, userID, postID int64, reactionType string) (ReactResult, error) {
	if reactionType == "" {
		reactionType = "heart"
	}

	var existing string
	err := s.DB.QueryRow(ctx,
		`SELECT reaction_type FROM reactions WHERE user_id = $1 AND post_id = $2`,
		userID, postID,
	).Scan(&existing)

	var result ReactResult
	switch {
	case err == pgx.ErrNoRows:
		_, err = s.DB.Exec(ctx,
			`INSERT INTO reactions (user_id, post_id, reaction_type) VALUES ($1, $2, $3)`,
			userID, postID, reactionType,
		)
		if err != nil {
			return "", fmt.Errorf("inserting reaction: %w", err)
		}
		result = ReactAdded

	case err != nil:
		return "", fmt.Errorf("reading reaction: %w", err)

	case existing == reactionType:
		_, err = s.DB.Exec(ctx,
			`DELETE FROM reactions WHERE user_id = $1 AND post_id = $2`,
			userID, postID,
		)
		if err != nil {
			return "", fmt.Errorf("removing reaction: %w", err)
		}
		result = ReactRemoved

	default:
		_, err = s.DB.Exec(ctx,
			`UPDATE reactions SET reaction_type = $3 WHERE user_id = $1 AND post_id = $2`,
			userID, postID, reactionType,
		)
		if err != nil {
			return "", fmt.Errorf("updating reaction: %w", err)
		}
		result = ReactSwitched
	}

	if _, err := s.DB.Exec(ctx,
		`UPDATE posts SET reaction_count = (SELECT COUNT(*) FROM reactions WHERE post_id = $1), updated_at = NOW() WHERE id = $1`,
		postID,
	); err != nil {
		return result, fmt.Errorf("updating reaction count: %w", err)
	}
	return result, nil
}

// React is the legacy idempotent add. Kept for callers that haven't
// been updated to Toggle yet. Delegates to Toggle which handles the
// same shape without creating duplicates.
func (s *ReactionStore) React(ctx context.Context, userID, postID int64, reactionType string) error {
	_, err := s.Toggle(ctx, userID, postID, reactionType)
	return err
}

func (s *ReactionStore) Unreact(ctx context.Context, userID, postID int64) error {
	_, err := s.DB.Exec(ctx,
		`DELETE FROM reactions WHERE user_id = $1 AND post_id = $2`,
		userID, postID,
	)
	if err != nil {
		return fmt.Errorf("unreacting: %w", err)
	}

	_, err = s.DB.Exec(ctx,
		`UPDATE posts SET reaction_count = (SELECT COUNT(*) FROM reactions WHERE post_id = $1), updated_at = NOW() WHERE id = $1`,
		postID,
	)
	if err != nil {
		return fmt.Errorf("updating reaction count: %w", err)
	}
	return nil
}

// HasReacted is kept for callsites that just want a boolean.
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

// UserReactionType returns the reaction type the user has on a post, or
// "" if they haven't reacted. Used by the client to pre-fill the picker.
func (s *ReactionStore) UserReactionType(ctx context.Context, userID, postID int64) (string, error) {
	var t string
	err := s.DB.QueryRow(ctx,
		`SELECT reaction_type FROM reactions WHERE user_id = $1 AND post_id = $2`,
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
