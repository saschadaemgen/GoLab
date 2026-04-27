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
	SpaceID       *int64    `json:"space_id,omitempty"`
	SeasonID      *int64    `json:"season_id,omitempty"` // Sprint 16b: post lives in a project season
	PostType      string    `json:"post_type"`
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
	SpaceName         string `json:"space_name,omitempty"`
	SpaceSlug         string `json:"space_slug,omitempty"`
	SpaceColor        string `json:"space_color,omitempty"`
	// Sprint 16b: joined season + project for the season-badge
	// rendered on post-card. Zero values when the post is a free-
	// space post (season_id is NULL).
	SeasonNumber int    `json:"season_number,omitempty"`
	SeasonTitle  string `json:"season_title,omitempty"`
	ProjectSlug  string `json:"project_slug,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`

	// Populated on demand (not by SELECT) - use TagStore.ListByPost.
	Tags []Tag `json:"tags,omitempty"`

	// Sprint 14 multi-reaction fields. Populated by feed / thread
	// handlers via ReactionStore.StateForMany; nil on naked reads
	// so templates must guard with `{{ if .ReactionCounts }}` or
	// use the fallback in post-card.html. Counts has every allowed
	// emoji type as key with a zero value when the post has none.
	ReactionCounts    map[string]int `json:"reaction_counts,omitempty"`
	UserReactionTypes []string       `json:"user_reaction_types,omitempty"`

	// Sprint 15a B6. Non-zero when the post has at least one row
	// in post_edit_history. The post-card / thread templates show
	// an "edited" badge next to CreatedAt when this is set. Never
	// populated by the default SELECT - handlers attach it on
	// demand via PostEditHistoryStore.LastEditAt.
	EditedAt *time.Time `json:"edited_at,omitempty"`
}

type PostStore struct {
	DB *pgxpool.Pool
}

// postSelectCols lists every column read by scanPosts(), in scan order.
// Joining spaces with LEFT JOIN keeps pre-migration-016 posts visible
// (space columns come back NULL). We COALESCE in SQL so the Go scan
// targets never see NULL for the string space fields.
//
// Sprint 16b: the trailing four columns add joined season + project
// metadata for posts assigned to a project season. NULL season_id
// (the common case) leaves them all zero / empty.
const postSelectCols = `
	p.id, p.as_type, p.author_id, p.channel_id, p.parent_id, p.repost_of_id,
	p.space_id, p.season_id, p.post_type, p.content, p.content_html,
	p.reaction_count, p.reply_count, p.repost_count,
	p.created_at, p.updated_at,
	u.username, u.display_name, u.avatar_url,
	COALESCE(s.name, ''), COALESCE(s.slug, ''), COALESCE(s.color, ''),
	COALESCE(se.season_number, 0), COALESCE(se.title, ''),
	COALESCE(pr.slug, ''), COALESCE(pr.name, '')`

const postJoins = `
	FROM posts p
	JOIN users u ON p.author_id = u.id
	LEFT JOIN spaces s ON p.space_id = s.id
	LEFT JOIN seasons se ON p.season_id = se.id
	LEFT JOIN projects pr ON se.project_id = pr.id`

// CreateParams bundles the optional post fields so adding a new one later
// doesn't break every call site.
type CreateParams struct {
	ASType      string // "Note", "Article", "Announce"
	AuthorID    int64
	ChannelID   *int64
	ParentID    *int64
	SpaceID     *int64
	SeasonID    *int64 // Sprint 16b: optional - posts can belong to a project season
	PostType    string // "discussion", "question", "tutorial", "code", "showcase", "link"
	Content     string
	ContentHTML string
}

// Create inserts a new post, bumps the parent reply_count if this is a
// reply, and returns the freshly-read row with all joined fields.
func (s *PostStore) Create(ctx context.Context, p CreateParams) (*Post, error) {
	if p.PostType == "" {
		p.PostType = "discussion"
	}

	var id int64
	err := s.DB.QueryRow(ctx,
		`INSERT INTO posts (as_type, author_id, channel_id, parent_id, space_id, season_id, post_type, content, content_html)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		p.ASType, p.AuthorID, p.ChannelID, p.ParentID, p.SpaceID, p.SeasonID, p.PostType, p.Content, p.ContentHTML,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("creating post: %w", err)
	}

	// Update reply count on parent if this is a reply.
	if p.ParentID != nil {
		if _, err := s.DB.Exec(ctx,
			`UPDATE posts SET reply_count = reply_count + 1, updated_at = NOW() WHERE id = $1`,
			*p.ParentID,
		); err != nil {
			return nil, fmt.Errorf("updating parent reply count: %w", err)
		}
	}

	// Read it back with all joined fields populated.
	return s.FindByID(ctx, id)
}

// CreateRepost makes an Announce-type post that points at `repostOfID`.
// The repost inherits the space_id of the original so it appears in the
// same space feed. post_type defaults to "discussion".
func (s *PostStore) CreateRepost(ctx context.Context, authorID int64, channelID *int64, repostOfID int64) (*Post, error) {
	var id int64
	err := s.DB.QueryRow(ctx,
		`INSERT INTO posts (as_type, author_id, channel_id, repost_of_id, content, space_id)
		 VALUES ('Announce', $1, $2, $3, '',
		         (SELECT space_id FROM posts WHERE id = $3))
		 RETURNING id`,
		authorID, channelID, repostOfID,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("creating repost: %w", err)
	}

	if _, err := s.DB.Exec(ctx,
		`UPDATE posts SET repost_count = repost_count + 1, updated_at = NOW() WHERE id = $1`,
		repostOfID,
	); err != nil {
		return nil, fmt.Errorf("updating repost count: %w", err)
	}

	return s.FindByID(ctx, id)
}

func (s *PostStore) FindByID(ctx context.Context, id int64) (*Post, error) {
	p := &Post{}
	row := s.DB.QueryRow(ctx,
		`SELECT `+postSelectCols+postJoins+` WHERE p.id = $1`, id)
	if err := scanPostRow(row, p); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("finding post by id: %w", err)
	}
	return p, nil
}

func (s *PostStore) ListByChannel(ctx context.Context, channelID int64, limit int, before *time.Time) ([]Post, error) {
	query := `SELECT ` + postSelectCols + postJoins +
		` WHERE p.channel_id = $1 AND p.parent_id IS NULL`

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
	query := `SELECT ` + postSelectCols + postJoins +
		` WHERE p.author_id = $1 AND p.parent_id IS NULL`

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
	query := `SELECT ` + postSelectCols + postJoins + `
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

// ListBySpace returns top-level posts in one space, optionally filtered
// by post type. postType == "" means all types.
func (s *PostStore) ListBySpace(ctx context.Context, spaceID int64, postType string, limit, offset int) ([]Post, error) {
	query := `SELECT ` + postSelectCols + postJoins + `
	           WHERE p.space_id = $1 AND p.parent_id IS NULL`
	args := []any{spaceID}
	if postType != "" {
		query += ` AND p.post_type = $4`
		args = append(args, limit, offset, postType)
	} else {
		args = append(args, limit, offset)
	}
	query += ` ORDER BY p.created_at DESC LIMIT $2 OFFSET $3`

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing posts by space: %w", err)
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ListBySeason returns top-level posts assigned to the given season.
// Sprint 16b: powers the season detail page. Order matches the rest of
// the post-list endpoints: newest first.
func (s *PostStore) ListBySeason(ctx context.Context, seasonID int64, limit, offset int) ([]Post, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+postSelectCols+postJoins+`
		 WHERE p.season_id = $1 AND p.parent_id IS NULL
		 ORDER BY p.created_at DESC LIMIT $2 OFFSET $3`,
		seasonID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing posts by season: %w", err)
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ListByTag returns top-level posts that have the given tag slug attached.
func (s *PostStore) ListByTag(ctx context.Context, tagSlug string, limit, offset int) ([]Post, error) {
	query := `SELECT ` + postSelectCols + postJoins + `
	           JOIN post_tags pt ON pt.post_id = p.id
	           JOIN tags t ON t.id = pt.tag_id
	           WHERE t.slug = $1 AND p.parent_id IS NULL
	           ORDER BY p.created_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.DB.Query(ctx, query, tagSlug, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing posts by tag: %w", err)
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ListBySpaceAndTag filters by both space and tag.
func (s *PostStore) ListBySpaceAndTag(ctx context.Context, spaceID int64, tagSlug string, limit, offset int) ([]Post, error) {
	query := `SELECT ` + postSelectCols + postJoins + `
	           JOIN post_tags pt ON pt.post_id = p.id
	           JOIN tags t ON t.id = pt.tag_id
	           WHERE p.space_id = $1 AND t.slug = $2 AND p.parent_id IS NULL
	           ORDER BY p.created_at DESC LIMIT $3 OFFSET $4`
	rows, err := s.DB.Query(ctx, query, spaceID, tagSlug, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing posts by space+tag: %w", err)
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ListReplies returns a flat list of replies to a post, sorted by created_at ASC.
// For Phase 1 we keep replies flat (depth=1) to avoid recursive queries; the
// schema supports arbitrary depth and we can upgrade to recursive CTEs later.
func (s *PostStore) ListReplies(ctx context.Context, parentID int64, limit int) ([]Post, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+postSelectCols+postJoins+`
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

// UpdateContentParams bundles everything PostStore.UpdateContent
// needs. ContentHTML is the already-sanitised (and mention-linked)
// HTML; raw Content mirrors the same field on Create - the server
// keeps the submitted form so future re-sanitisation passes don't
// lose fidelity. EditKind is PostEditKindAuthor for self-edits and
// PostEditKindAdmin for moderation edits (Sprint 15c).
type UpdateContentParams struct {
	PostID      int64
	EditorID    int64
	Content     string
	ContentHTML string
	EditKind    string
	EditReason  string
}

// UpdateContent writes a post_edit_history row and overwrites the
// posts row in a single transaction. Returns the updated Post with
// the caller-visible joined fields populated (so callers don't
// need a separate FindByID roundtrip). Returns an error if the
// post does not exist or the editor is neither the author nor an
// admin-marked caller; authorisation is enforced at the handler,
// this method only touches rows.
func (s *PostStore) UpdateContent(ctx context.Context, p UpdateContentParams) (*Post, error) {
	if p.EditKind == "" {
		p.EditKind = PostEditKindAuthor
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("update post: begin tx: %w", err)
	}
	// Rollback on any non-commit exit. A successful Commit below
	// renders this Rollback a no-op.
	defer tx.Rollback(ctx)

	// Snapshot the previous content BEFORE we overwrite. One row
	// lock here prevents a racing edit from landing a stale
	// history entry.
	var prev string
	if err := tx.QueryRow(ctx,
		`SELECT content FROM posts WHERE id = $1 FOR UPDATE`, p.PostID,
	).Scan(&prev); err == pgx.ErrNoRows {
		return nil, fmt.Errorf("post not found")
	} else if err != nil {
		return nil, fmt.Errorf("update post: read previous: %w", err)
	}

	// Insert the history row with the previous content.
	hist := &PostEditHistoryStore{}
	_, err = hist.Record(ctx, tx, PostEditHistory{
		PostID:          p.PostID,
		EditorID:        p.EditorID,
		PreviousContent: prev,
		EditReason:      p.EditReason,
		EditKind:        p.EditKind,
	})
	if err != nil {
		return nil, err
	}

	// Overwrite the posts row.
	if _, err := tx.Exec(ctx,
		`UPDATE posts
		    SET content = $2, content_html = $3, updated_at = NOW()
		  WHERE id = $1`,
		p.PostID, p.Content, p.ContentHTML,
	); err != nil {
		return nil, fmt.Errorf("update post: exec: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("update post: commit: %w", err)
	}

	// Re-fetch with joined fields so the handler's response has
	// everything a re-render needs (author username / display name,
	// space metadata, etc.).
	return s.FindByID(ctx, p.PostID)
}

// scanPostRow reads a single row in the canonical postSelectCols order.
// Used by FindByID which calls DB.QueryRow.
func scanPostRow(row pgx.Row, p *Post) error {
	return row.Scan(
		&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
		&p.SpaceID, &p.SeasonID, &p.PostType, &p.Content, &p.ContentHTML,
		&p.ReactionCount, &p.ReplyCount, &p.RepostCount,
		&p.CreatedAt, &p.UpdatedAt,
		&p.AuthorUsername, &p.AuthorDisplayName, &p.AuthorAvatarURL,
		&p.SpaceName, &p.SpaceSlug, &p.SpaceColor,
		&p.SeasonNumber, &p.SeasonTitle, &p.ProjectSlug, &p.ProjectName,
	)
}

// scanPosts drains an rows iterator using the canonical postSelectCols
// order. Must match postSelectCols exactly.
func scanPosts(rows pgx.Rows) ([]Post, error) {
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(
			&p.ID, &p.ASType, &p.AuthorID, &p.ChannelID, &p.ParentID, &p.RepostOfID,
			&p.SpaceID, &p.SeasonID, &p.PostType, &p.Content, &p.ContentHTML,
			&p.ReactionCount, &p.ReplyCount, &p.RepostCount,
			&p.CreatedAt, &p.UpdatedAt,
			&p.AuthorUsername, &p.AuthorDisplayName, &p.AuthorAvatarURL,
			&p.SpaceName, &p.SpaceSlug, &p.SpaceColor,
			&p.SeasonNumber, &p.SeasonTitle, &p.ProjectSlug, &p.ProjectName,
		); err != nil {
			return nil, fmt.Errorf("scanning post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}
