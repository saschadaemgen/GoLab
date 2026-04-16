package model

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReactionStore struct {
	DB *pgxpool.Pool
}

func (s *ReactionStore) React(ctx context.Context, userID, postID int64, reactionType string) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO reactions (user_id, post_id, reaction_type) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, postID, reactionType,
	)
	if err != nil {
		return fmt.Errorf("reacting: %w", err)
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
