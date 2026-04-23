package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/config"
	"github.com/saschadaemgen/GoLab/internal/database"
	"github.com/saschadaemgen/GoLab/internal/handler"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// perUserRate returns an httprate key function that buckets requests by
// the authenticated user id. It requires an upstream RequireAuth so the
// user is in context; falls back to real IP if for any reason it isn't.
func perUserRate(r *http.Request) (string, error) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return fmt.Sprintf("user:%d", u.ID), nil
	}
	return httprate.KeyByRealIP(r)
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	slog.Info("starting GoLab",
		"env", cfg.Env,
		"addr", cfg.Addr(),
	)

	// Run migrations
	if err := database.Migrate(cfg.DB.ConnString(), "internal/database/migrations"); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	// Connect to database
	ctx := context.Background()
	pool, err := database.Connect(ctx, cfg.DB.ConnString())
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	// Template engine
	tmpls, err := render.New("web/templates")
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}

	// Markdown renderer (kept for old posts and Markdown-submitting API clients).
	md := render.NewMarkdown()

	// HTML sanitizer - applied to everything Quill/Markdown produces.
	sanitizer := render.NewSanitizer()

	// WebSocket hub
	hub := handler.NewHub(tmpls)
	hubCtx, hubCancel := context.WithCancel(ctx)
	defer hubCancel()
	go hub.Run(hubCtx)

	// Sprint 13: backup-at-rest encryption. Initialised before the
	// router so a bad GOLAB_BACKUP_KEY fails the whole startup
	// rather than letting the server come up without encrypted
	// backups. An empty key triggers one-shot generation that's
	// logged once by NewBackupCrypto.
	backupCrypto, err := handler.NewBackupCrypto(cfg.BackupKey)
	if err != nil {
		return fmt.Errorf("backup crypto: %w", err)
	}

	// Build router
	r := newRouter(cfg, pool, tmpls, md, sanitizer, hub, backupCrypto)

	// Start server
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("server listening", "addr", cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}

func newRouter(cfg *config.Config, pool *pgxpool.Pool, tmpls *render.Engine, md *render.Markdown, sanitizer *render.Sanitizer, hub *handler.Hub, backupCrypto *handler.BackupCrypto) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(handler.SecurityHeaders)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Stores
	users := &model.UserStore{DB: pool}
	channels := &model.ChannelStore{DB: pool}
	posts := &model.PostStore{DB: pool}
	follows := &model.FollowStore{DB: pool}
	reactions := &model.ReactionStore{DB: pool}
	notifs := &model.NotificationStore{DB: pool}
	spaces := &model.SpaceStore{DB: pool}
	tags := &model.TagStore{DB: pool}
	settings := &model.SettingsStore{DB: pool}
	sessions := &auth.SessionStore{DB: pool}
	// Sprint 14: MentionStore borrows UserStore for resolution.
	mentions := &model.MentionStore{DB: pool, Users: users}
	// Sprint 15a B6: per-post edit history persisted on every
	// author self-edit (and, from Sprint 15c onward, every admin
	// moderation edit).
	editHistory := &model.PostEditHistoryStore{DB: pool}

	// Notification dispatcher (used by post + profile handlers to fan events).
	notifDispatch := &handler.NotifDispatch{Store: notifs, Hub: hub}

	// Auth middleware
	requireAuth := auth.RequireAuth(sessions, users)
	optionalAuth := auth.OptionalAuth(sessions, users)
	requireAuthRedirect := auth.RequireAuthRedirect(sessions, users)

	// API handlers
	home := &handler.HomeHandler{DB: pool}
	authH := &handler.AuthHandler{
		Users:         users,
		Sessions:      sessions,
		Settings:      settings,
		Notifications: notifs,
		Hub:           hub,
		Secure:        !cfg.IsDev(),
	}
	channelH := &handler.ChannelHandler{Channels: channels, Users: users}
	postH := &handler.PostHandler{
		Posts: posts, Channels: channels, Reactions: reactions, Tags: tags,
		Spaces:      spaces,
		Users:       users,       // Sprint 14: resolve @mentions -> profile links
		Mentions:    mentions,    // Sprint 14: record mention rows on Create / Update
		EditHistory: editHistory, // Sprint 15a B6: LastEditAt for the edited badge
		Markdown:    md, Sanitizer: sanitizer, Hub: hub, Notifs: notifDispatch,
	}
	imageH := &handler.ImageHandler{DB: pool, RootDir: "web/static"}
	spaceH := &handler.SpaceHandler{Render: tmpls, Spaces: spaces, Posts: posts, Tags: tags, Reactions: reactions, EditHistory: editHistory}
	tagH := &handler.TagHandler{Render: tmpls, Tags: tags, Posts: posts, Spaces: spaces, Reactions: reactions, EditHistory: editHistory}
	feedH := &handler.FeedHandler{Posts: posts, Reactions: reactions, EditHistory: editHistory}
	profileH := &handler.ProfileHandler{
		Users:    users,
		Posts:    posts,
		Follows:  follows,
		Sessions: sessions,
		Settings: settings,
		Notifs:   notifDispatch,
	}
	notifH := &handler.NotifHandler{Store: notifs}
	avatarH := &handler.AvatarHandler{Users: users, RootDir: "web/static"}
	searchH := &handler.SearchHandler{DB: pool}
	adminH := &handler.AdminHandler{
		DB: pool, Render: tmpls, Users: users,
		Settings: settings, Notifications: notifs, Hub: hub,
	}
	// Sprint 13: admin database management (backup, export, import).
	// BackupDir defaults to /opt/backups inside the container; the
	// docker-compose `golab-backups` volume mounts it there. The
	// BackupCrypto is constructed in run() before the router is
	// built so startup fails closed on a bad GOLAB_BACKUP_KEY.
	dbH := &handler.DBHandler{Cfg: cfg, Crypto: backupCrypto}

	// Page handlers
	pageH := &handler.PageHandler{
		Render:      tmpls,
		Users:       users,
		Channels:    channels,
		Posts:       posts,
		Follows:     follows,
		Reactions:   reactions,
		Spaces:      spaces,
		Settings:    settings,
		EditHistory: editHistory, // Sprint 15a B6: edited_at on feed/thread/profile
		SiteName:    cfg.SiteName,
	}

	// Static files
	fileServer := http.FileServer(http.Dir("web/static"))
	r.Handle("/static/*", http.StripPrefix("/static/", fileServer))

	// WebSocket endpoint
	r.Get("/ws", hub.HandleWS(sessions, users))

	// HTML page routes
	r.Group(func(r chi.Router) {
		r.Use(optionalAuth)
		r.Get("/", pageH.Home)
		r.Get("/register", pageH.RegisterPage)
		r.Get("/login", pageH.LoginPage)
		r.Get("/explore", pageH.ExplorePage)
		// Sprint 13: /c/{slug} is a legacy redirect - Spaces replaced
		// Channels as the primary UI unit. The handler itself is a
		// one-liner http.Redirect.
		r.Get("/c/{slug}", pageH.ChannelPage)
		r.Get("/u/{username}", pageH.ProfilePage)
		r.Get("/p/{id}", pageH.ThreadPage)
		r.Get("/s/{slug}", spaceH.SpacePage)
		r.Get("/t/{slug}", tagH.TagPage)
	})

	r.Group(func(r chi.Router) {
		r.Use(requireAuthRedirect)
		r.Get("/feed", pageH.FeedPage)
		r.Get("/settings", pageH.SettingsPage)
		r.Get("/pending", pageH.PendingPage)
		// POST /settings is the no-JS fallback for the settings form.
		// HTML forms can't use PUT, so we expose a POST alias that
		// reuses the same handler. UpdateMe reads form-encoded bodies
		// and redirects back to /settings on success.
		r.Post("/settings", profileH.UpdateMe)
	})

	// Admin page (HTML)
	r.Group(func(r chi.Router) {
		r.Use(requireAuthRedirect)
		r.Use(handler.RequireAdmin)
		r.Get("/admin", adminH.Page)
	})

	// API routes
	r.Route("/api", func(r chi.Router) {
		r.Get("/health", home.Health)

		// Auth (public) - IP-bucketed rate limits to slow brute force and
		// mass-signup abuse.
		r.Group(func(r chi.Router) {
			r.Use(httprate.Limit(5, time.Hour,
				httprate.WithKeyFuncs(httprate.KeyByRealIP),
				httprate.WithLimitHandler(handler.RateLimited),
			))
			r.Post("/register", authH.Register)
		})
		r.Group(func(r chi.Router) {
			r.Use(httprate.Limit(10, time.Minute,
				httprate.WithKeyFuncs(httprate.KeyByRealIP),
				httprate.WithLimitHandler(handler.RateLimited),
			))
			r.Post("/login", authH.Login)
		})

		// Auth (protected)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/logout", authH.Logout)
			r.Get("/me", authH.Me)
		})

		// Preview (Markdown render, public so the landing could use it too)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/preview", postH.Preview)
		})

		// Channels - reads are open, mutations require an active (not
		// pending / rejected) user.
		r.Get("/channels", channelH.List)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/channels/{slug}", channelH.Get)
		})
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(auth.RequireActiveUser)
			r.Post("/channels", channelH.Create)
			r.Post("/channels/{slug}/join", channelH.Join)
			r.Post("/channels/{slug}/leave", channelH.Leave)
		})

		// Posts (protected). 30 creates/minute per user is fine for humans;
		// the limiter stops a runaway script and broken client loops.
		// Reads (GET /posts/{id}) stay accessible to pending users.
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/posts/{id}", postH.Get)
		})
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(auth.RequireActiveUser)
			r.With(httprate.Limit(30, time.Minute,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Post("/posts", postH.Create)
			// Sprint 15a B6: author self-edit. Admin path will live
			// under /admin/posts/{id} in Sprint 15c.
			// Sprint 15a B7 Bug 4: same 30/min/user rate limit as
			// Create. The edit path is actually more expensive than
			// create (sanitise + mention-extract + history-insert in
			// a transaction), so leaving it uncapped was a DoS vector.
			r.With(httprate.Limit(30, time.Minute,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Patch("/posts/{id}", postH.Update)
			r.Delete("/posts/{id}", postH.Delete)
			r.Post("/posts/{id}/react", postH.React)
			r.Delete("/posts/{id}/react", postH.Unreact)
			r.Post("/posts/{id}/repost", postH.Repost)
		})

		// Feed (protected)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/feed", feedH.Get)
		})

		// Profiles: GET is public. Own-profile mutations and follow
		// actions require an active user.
		r.Get("/users/{username}", profileH.Get)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(auth.RequireActiveUser)
			r.Put("/users/me", profileH.UpdateMe)
			// Sprint 13: password change. 3/hour per user stops brute-force
			// current-password guessing and accidental request storms from
			// a broken client. Revokes all sessions on success.
			r.With(httprate.Limit(3, time.Hour,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Post("/users/me/password", profileH.ChangePassword)
			// Sprint 13: live username availability probe. 60/minute per
			// user is plenty for a debounced typeahead and cheap enough
			// to leave uncontested.
			r.With(httprate.Limit(60, time.Minute,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Get("/users/check-username", profileH.CheckUsername)
			// Sprint 14: @mention autocomplete. 60/min/user is plenty
			// for a debounced typeahead and keeps the prefix-ILIKE
			// query from becoming a DoS vector.
			r.With(httprate.Limit(60, time.Minute,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Get("/users/autocomplete", profileH.Autocomplete)
			r.With(httprate.Limit(10, time.Hour,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Post("/users/me/avatar", avatarH.Upload)
			r.Delete("/users/me/avatar", avatarH.Remove)
			r.Post("/users/{username}/follow", profileH.Follow)
			r.Delete("/users/{username}/follow", profileH.Unfollow)
		})

		// Image upload (Quill editor). 10/hour per user. Expensive due to
		// decode+resize; we never want one user to saturate the server.
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(auth.RequireActiveUser)
			r.Use(httprate.Limit(10, time.Hour,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			))
			r.Post("/upload/image", imageH.Upload)
		})

		// Notifications (protected)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/notifications", notifH.List)
			r.Get("/notifications/count", notifH.Count)
			r.Post("/notifications/read-all", notifH.MarkAllRead)
			r.Post("/notifications/{id}/read", notifH.MarkRead)
		})

		// Search (protected - only logged-in users can search)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/search", searchH.Search)
		})

		// Spaces + tags (public read)
		r.Get("/spaces", spaceH.List)
		r.Get("/tags/search", tagH.Search)

		// Admin (power_level >= 75 via RequireAdmin - Sprint 12 admin
		// tasks include moderation which is a trust floor below owner).
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(handler.RequireAdmin)
			r.Get("/admin/stats", adminH.Stats)
			r.Get("/admin/users", adminH.ListUsers)
			r.Post("/admin/users/{id}/ban", adminH.Ban)
			r.Post("/admin/users/{id}/unban", adminH.Unban)
			r.Put("/admin/users/{id}/power", adminH.SetPower)
			// Sprint 13: admins can rename any user below their level,
			// regardless of the allow_username_change toggle.
			r.Put("/admin/users/{id}/username", adminH.SetUsername)
			// Sprint 12 moderation
			r.Get("/admin/pending", adminH.Pending)
			r.Post("/admin/users/{id}/approve", adminH.Approve)
			r.Post("/admin/users/{id}/reject", adminH.Reject)
			r.Put("/admin/settings/{key}", adminH.SetSetting)

			// Sprint 13: database management. Backup / Export / List /
			// per-file Download need admin (75+); Import needs Owner
			// (100, enforced inside the handler). pg_dump and psql
			// are expensive; the handler timeouts keep abuse bounded.
			r.Get("/admin/db/backups", dbH.ListBackups)
			r.Get("/admin/db/backups/{filename}/download", dbH.DownloadBackup)
			r.Delete("/admin/db/backups/{filename}", dbH.DeleteBackup)
			r.Post("/admin/db/backup", dbH.Backup)
			r.Get("/admin/db/export", dbH.Export)
			r.Post("/admin/db/import", dbH.Import)
		})
	})

	return r
}
