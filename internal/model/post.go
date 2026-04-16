package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Post struct {
	ID            int64     `json:"id"`
	ASType        string    `json:"as_type"`
	AuthorID      int64     `json:"author_id"`
	ChannelID     *int64    `json:"channel_id,omitempty"`
	ParentID      *int64    `json:"parent_id,omitempty"`
	RepostOfID    *int64    `json:"repost_of_id,omitempty"`
	Content       string    `json:"content"`
	ContentHTML   string    `json:"content_html"`
	ReactionCount int       `json:"reaction_count"`
	ReplyCount    int       `json:"reply_count"`
	RepostCount   int       `json:"repost_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Joined fields (not always populated)
	AuthorUsername    string `json:"author_username,omitempty"`
	AuthorDisplayName string `json:"author_display_name,omitempty"`
	AuthorAvatarURL   string `json:"author_avatar_url,omitempty"`
}

type PostStore struct {
	DB *pgxpool.Pool
}

func (s *PostStore) Create(ctx context.Context, asType string, authorID int64, channelID, parentID *int64, content, contentHTML string) (*Post, error) {
	p := &Post{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO posts (as_type, author_id, channel_id, parent_id, content, content_html)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, as_type, author_id, channel_id, parent_id, repost_of_id, content, content_html,
		           reaction_count, reply_count, repost_count, created_at, updated_at`,
		asType, authorID, channelID, parentID, content, contentHTML,
	).Scan(
		&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
		&p.Content, &p.ContentHTML, &p.ReactionCount, &p.ReplyCount, &p.RepostCount,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating post: %w", err)
	}

	// Update reply count on parent if this is a reply
	if parentID != nil {
		_, err = s.DB.Exec(ctx,
			`UPDATE posts SET reply_count = reply_count + 1, updated_at = NOW() WHERE id = $1`,
			*parentID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating parent reply count: %w", err)
		}
	}

	return p, nil
}

func (s *PostStore) CreateRepost(ctx context.Context, authorID int64, channelID *int64, repostOfID int64) (*Post, error) {
	p := &Post{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO posts (as_type, author_id, channel_id, repost_of_id, content)
		 VALUES ('Announce', $1, $2, $3, '')
		 RETURNING id, as_type, author_id, channel_id, parent_id, repost_of_id, content, content_html,
		           reaction_count, reply_count, repost_count, created_at, updated_at`,
		authorID, channelID, repostOfID,
	).Scan(
		&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
		&p.Content, &p.ContentHTML, &p.ReactionCount, &p.ReplyCount, &p.RepostCount,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating repost: %w", err)
	}

	// Update repost count on original
	_, err = s.DB.Exec(ctx,
		`UPDATE posts SET repost_count = repost_count + 1, updated_at = NOW() WHERE id = $1`,
		repostOfID,
	)
	if err != nil {
		return nil, fmt.Errorf("updating repost count: %w", err)
	}

	return p, nil
}

func (s *PostStore) FindByID(ctx context.Context, id int64) (*Post, error) {
	p := &Post{}
	err := s.DB.QueryRow(ctx,
		`SELECT p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
		        p.content, p.content_html, p.reaction_count, p.reply_count, p.repost_count,
		        p.created_at, p.updated_at,
		        u.username, u.display_name, u.avatar_url
		 FROM posts p JOIN users u ON p.author_id = u.id
		 WHERE p.id = $1`,
		id,
	).Scan(
		&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
		&p.Content, &p.ContentHTML, &p.ReactionCount, &p.ReplyCount, &p.RepostCount,
		&p.CreatedAt, &p.UpdatedAt,
		&p.AuthorUsername, &p.AuthorDisplayName, &p.AuthorAvatarURL,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding post by id: %w", err)
	}
	return p, nil
}

func (s *PostStore) ListByChannel(ctx context.Context, channelID int64, limit int, before *time.Time) ([]Post, error) {
	query := `SELECT p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
	                  p.content, p.content_html, p.reaction_count, p.reply_count, p.repost_count,
	                  p.created_at, p.updated_at,
	                  u.username, u.display_name, u.avatar_url
	           FROM posts p JOIN users u ON p.author_id = u.id
	           WHERE p.channel_id = $1 AND p.parent_id IS NULL`

	args := []any{channelID}
	if before != nil {
		query += ` AND p.created_at < $3`
		args = append(args, limit, *before)
	} else {
		args = append(args, limit)
	}

	query += ` ORDER BY p.created_at DESC LIMIT $2`

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing posts by channel: %w", err)
	}
	defer rows.Close()

	return scanPosts(rows)
}

func (s *PostStore) ListByAuthor(ctx context.Context, authorID int64, limit int, before *time.Time) ([]Post, error) {
	query := `SELECT p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
	                  p.content, p.content_html, p.reaction_count, p.reply_count, p.repost_count,
	                  p.created_at, p.updated_at,
	                  u.username, u.display_name, u.avatar_url
	           FROM posts p JOIN users u ON p.author_id = u.id
	           WHERE p.author_id = $1 AND p.parent_id IS NULL`

	args := []any{authorID}
	if before != nil {
		query += ` AND p.created_at < $3`
		args = append(args, limit, *before)
	} else {
		args = append(args, limit)
	}

	query += ` ORDER BY p.created_at DESC LIMIT $2`

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing posts by author: %w", err)
	}
	defer rows.Close()

	return scanPosts(rows)
}

func (s *PostStore) Feed(ctx context.Context, userID int64, limit int, before *time.Time) ([]Post, error) {
	query := `SELECT p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
	                  p.content, p.reaction_count, p.reply_count, p.repost_count,
	                  p.created_at, p.updated_at,
	                  u.username, u.display_name, u.avatar_url
	           FROM posts p
	           JOIN users u ON p.author_id = u.id
	           WHERE p.parent_id IS NULL
	             AND (
	               p.channel_id IN (SELECT channel_id FROM channel_members WHERE user_id = $1)
	               OR p.author_id IN (SELECT following_id FROM follows WHERE follower_id = $1)
	               OR p.author_id = $1
	             )`

	args := []any{userID}
	if before != nil {
		query += ` AND p.created_at < $3`
		args = append(args, limit, *before)
	} else {
		args = append(args, limit)
	}

	query += ` ORDER BY p.created_at DESC LIMIT $2`

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}
	defer rows.Close()

	return scanPosts(rows)
}

// ListReplies returns a flat list of replies to a post, sorted by created_at ASC.
// For Phase 1 we keep replies flat (depth=1) to avoid recursive queries; the
// schema supports arbitrary depth and we can upgrade to recursive CTEs later.
func (s *PostStore) ListReplies(ctx context.Context, parentID int64, limit int) ([]Post, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
		        p.content, p.content_html, p.reaction_count, p.reply_count, p.repost_count,
		        p.created_at, p.updated_at,
		        u.username, u.display_name, u.avatar_url
		 FROM posts p JOIN users u ON p.author_id = u.id
		 WHERE p.parent_id = $1
		 ORDER BY p.created_at ASC
		 LIMIT $2`,
		parentID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing replies: %w", err)
	}
	defer rows.Close()
	return scanPosts(rows)
}

func (s *PostStore) Delete(ctx context.Context, id, authorID int64) error {
	result, err := s.DB.Exec(ctx,
		`DELETE FROM posts WHERE id = $1 AND author_id = $2`,
		id, authorID,
	)
	if err != nil {
		return fmt.Errorf("deleting post: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("post not found or not owned by user")
	}
	return nil
}

func scanPosts(rows pgx.Rows) ([]Post, error) {
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(
			&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
			&p.Content, &p.ContentHTML, &p.ReactionCount, &p.ReplyCount, &p.RepostCount,
			&p.CreatedAt, &p.UpdatedAt,
			&p.AuthorUsername, &p.AuthorDisplayName, &p.AuthorAvatarURL,
		); err != nil {
			return nil, fmt.Errorf("scanning post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}
