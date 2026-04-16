package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification types (mirror on client for rendering).
const (
	NotifReaction = "reaction"
	NotifReply    = "reply"
	NotifFollow   = "follow"
	NotifMention  = "mention"
)

type Notification struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	ActorID   int64     `json:"actor_id"`
	Type      string    `json:"notif_type"`
	PostID    *int64    `json:"post_id,omitempty"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`

	// Joined / computed fields (not stored)
	ActorUsername    string `json:"actor_username,omitempty"`
	ActorDisplayName string `json:"actor_display_name,omitempty"`
	ActorAvatarURL   string `json:"actor_avatar_url,omitempty"`
	PostExcerpt      string `json:"post_excerpt,omitempty"`
}

type NotificationStore struct {
	DB *pgxpool.Pool
}

func (s *NotificationStore) Create(ctx context.Context, userID, actorID int64, notifType string, postID *int64) (*Notification, error) {
	// Don't notify yourself (self-reactions, self-replies)
	if userID == actorID {
		return nil, nil
	}

	// Collapse duplicates: same actor reacting to same post within 60s counts once.
	// For follow notifications, collapse within 1h so refresh-spam doesn't pile up.
	if notifType == NotifReaction && postID != nil {
		var exists bool
		err := s.DB.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM notifications
			   WHERE user_id = $1 AND actor_id = $2 AND notif_type = $3 AND post_id = $4
			     AND created_at > NOW() - INTERVAL '60 seconds'
			 )`,
			userID, actorID, notifType, *postID,
		).Scan(&exists)
		if err == nil && exists {
			return nil, nil
		}
	}
	if notifType == NotifFollow {
		var exists bool
		err := s.DB.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM notifications
			   WHERE user_id = $1 AND actor_id = $2 AND notif_type = $3
			     AND created_at > NOW() - INTERVAL '1 hour'
			 )`,
			userID, actorID, notifType,
		).Scan(&exists)
		if err == nil && exists {
			return nil, nil
		}
	}

	n := &Notification{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO notifications (user_id, actor_id, notif_type, post_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, actor_id, notif_type, post_id, is_read, created_at`,
		userID, actorID, notifType, postID,
	).Scan(&n.ID, &n.UserID, &n.ActorID, &n.Type, &n.PostID, &n.IsRead, &n.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating notification: %w", err)
	}
	return n, nil
}

// List returns the most recent notifications for a user, joined with actor
// and post excerpt for display.
func (s *NotificationStore) List(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT n.id, n.user_id, n.actor_id, n.notif_type, n.post_id, n.is_read, n.created_at,
		        u.username, u.display_name, u.avatar_url,
		        COALESCE(SUBSTRING(p.content FROM 1 FOR 80), '') AS excerpt
		 FROM notifications n
		 JOIN users u ON u.id = n.actor_id
		 LEFT JOIN posts p ON p.id = n.post_id
		 WHERE n.user_id = $1
		 ORDER BY n.created_at DESC
		 LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing notifications: %w", err)
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.ActorID, &n.Type, &n.PostID, &n.IsRead, &n.CreatedAt,
			&n.ActorUsername, &n.ActorDisplayName, &n.ActorAvatarURL, &n.PostExcerpt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *NotificationStore) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = FALSE`,
		userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting notifications: %w", err)
	}
	return count, nil
}

func (s *NotificationStore) MarkAllRead(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE notifications SET is_read = TRUE WHERE user_id = $1 AND is_read = FALSE`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("marking all read: %w", err)
	}
	return nil
}

func (s *NotificationStore) MarkRead(ctx context.Context, userID, id int64) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE notifications SET is_read = TRUE WHERE id = $1 AND user_id = $2`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("marking read: %w", err)
	}
	return nil
}
