package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SearchHandler struct {
	DB *pgxpool.Pool
}

type searchResult struct {
	Posts    []searchPost    `json:"posts"`
	Channels []searchChannel `json:"channels"`
	Users    []searchUser    `json:"users"`
}

type searchPost struct {
	ID             int64     `json:"id"`
	Content        string    `json:"content"`
	AuthorUsername string    `json:"author_username"`
	CreatedAt      time.Time `json:"created_at"`
}

type searchChannel struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

type searchUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, searchResult{
			Posts: []searchPost{}, Channels: []searchChannel{}, Users: []searchUser{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	out := searchResult{
		Posts:    []searchPost{},
		Channels: []searchChannel{},
		Users:    []searchUser{},
	}

	// Posts: full-text search
	rows, err := h.DB.Query(ctx, `
		SELECT p.id, p.content, u.username, p.created_at
		FROM posts p JOIN users u ON p.author_id = u.id
		WHERE p.search_vector @@ plainto_tsquery('english', $1)
		   OR p.content ILIKE '%' || $1 || '%'
		ORDER BY ts_rank(p.search_vector, plainto_tsquery('english', $1)) DESC,
		         p.created_at DESC
		LIMIT 8
	`, q)
	if err != nil {
		slog.Error("search posts", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p searchPost
			if err := rows.Scan(&p.ID, &p.Content, &p.AuthorUsername, &p.CreatedAt); err == nil {
				out.Posts = append(out.Posts, p)
			}
		}
	}

	// Channels: slug/name ILIKE
	like := "%" + q + "%"
	crows, err := h.DB.Query(ctx, `
		SELECT id, slug, name, member_count
		FROM channels
		WHERE slug ILIKE $1 OR name ILIKE $1
		ORDER BY member_count DESC
		LIMIT 6
	`, like)
	if err != nil {
		slog.Error("search channels", "error", err)
	} else {
		defer crows.Close()
		for crows.Next() {
			var c searchChannel
			if err := crows.Scan(&c.ID, &c.Slug, &c.Name, &c.MemberCount); err == nil {
				out.Channels = append(out.Channels, c)
			}
		}
	}

	// Users: username/display_name ILIKE
	urows, err := h.DB.Query(ctx, `
		SELECT id, username, display_name, avatar_url
		FROM users
		WHERE username ILIKE $1 OR display_name ILIKE $1
		ORDER BY power_level DESC, username ASC
		LIMIT 6
	`, like)
	if err != nil {
		slog.Error("search users", "error", err)
	} else {
		defer urows.Close()
		for urows.Next() {
			var u searchUser
			if err := urows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.AvatarURL); err == nil {
				out.Users = append(out.Users, u)
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

