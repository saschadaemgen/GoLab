package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Follow struct {
	FollowerID  int64     `json:"follower_id"`
	FollowingID int64     `json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type FollowStore struct {
	DB *pgxpool.Pool
}

func (s *FollowStore) Follow(ctx context.Context, followerID, followingID int64) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO follows (follower_id, following_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		followerID, followingID,
	)
	if err != nil {
		return fmt.Errorf("following: %w", err)
	}
	return nil
}

func (s *FollowStore) Unfollow(ctx context.Context, followerID, followingID int64) error {
	_, err := s.DB.Exec(ctx,
		`DELETE FROM follows WHERE follower_id = $1 AND following_id = $2`,
		followerID, followingID,
	)
	if err != nil {
		return fmt.Errorf("unfollowing: %w", err)
	}
	return nil
}

func (s *FollowStore) IsFollowing(ctx context.Context, followerID, followingID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND following_id = $2)`,
		followerID, followingID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking follow: %w", err)
	}
	return exists, nil
}

func (s *FollowStore) FollowerCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM follows WHERE following_id = $1`,
		userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting followers: %w", err)
	}
	return count, nil
}

func (s *FollowStore) FollowingCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM follows WHERE follower_id = $1`,
		userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting following: %w", err)
	}
	return count, nil
}
