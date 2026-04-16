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

	// Build router
	r := newRouter(cfg, pool, tmpls, md, sanitizer, hub)

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

func newRouter(cfg *config.Config, pool *pgxpool.Pool, tmpls *render.Engine, md *render.Markdown, sanitizer *render.Sanitizer, hub *handler.Hub) *chi.Mux {
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
	sessions := &auth.SessionStore{DB: pool}

	// Notification dispatcher (used by post + profile handlers to fan events).
	notifDispatch := &handler.NotifDispatch{Store: notifs, Hub: hub}

	// Auth middleware
	requireAuth := auth.RequireAuth(sessions, users)
	optionalAuth := auth.OptionalAuth(sessions, users)
	requireAuthRedirect := auth.RequireAuthRedirect(sessions, users)

	// API handlers
	home := &handler.HomeHandler{DB: pool}
	authH := &handler.AuthHandler{
		Users:    users,
		Sessions: sessions,
		Secure:   !cfg.IsDev(),
	}
	channelH := &handler.ChannelHandler{Channels: channels, Users: users}
	postH := &handler.PostHandler{
		Posts: posts, Channels: channels, Reactions: reactions,
		Markdown: md, Sanitizer: sanitizer, Hub: hub, Notifs: notifDispatch,
	}
	imageH := &handler.ImageHandler{DB: pool, RootDir: "web/static"}
	feedH := &handler.FeedHandler{Posts: posts}
	profileH := &handler.ProfileHandler{Users: users, Posts: posts, Follows: follows, Notifs: notifDispatch}
	notifH := &handler.NotifHandler{Store: notifs}
	avatarH := &handler.AvatarHandler{Users: users, RootDir: "web/static"}
	searchH := &handler.SearchHandler{DB: pool}
	adminH := &handler.AdminHandler{DB: pool, Render: tmpls, Users: users}

	// Page handlers
	pageH := &handler.PageHandler{
		Render:    tmpls,
		Users:     users,
		Channels:  channels,
		Posts:     posts,
		Follows:   follows,
		Reactions: reactions,
		SiteName:  cfg.SiteName,
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
		r.Get("/c/{slug}", pageH.ChannelPage)
		r.Get("/u/{username}", pageH.ProfilePage)
		r.Get("/p/{id}", pageH.ThreadPage)
	})

	r.Group(func(r chi.Router) {
		r.Use(requireAuthRedirect)
		r.Get("/feed", pageH.FeedPage)
		r.Get("/settings", pageH.SettingsPage)
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

		// Channels
		r.Get("/channels", channelH.List)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/channels", channelH.Create)
			r.Get("/channels/{slug}", channelH.Get)
			r.Post("/channels/{slug}/join", channelH.Join)
			r.Post("/channels/{slug}/leave", channelH.Leave)
		})

		// Posts (protected). 30 creates/minute per user is fine for humans;
		// the limiter stops a runaway script and broken client loops.
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.With(httprate.Limit(30, time.Minute,
				httprate.WithKeyFuncs(perUserRate),
				httprate.WithLimitHandler(handler.RateLimited),
			)).Post("/posts", postH.Create)
			r.Get("/posts/{id}", postH.Get)
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

		// Profiles
		r.Get("/users/{username}", profileH.Get)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Put("/users/me", profileH.UpdateMe)
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

		// Admin (power_level >= 100)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(handler.RequireAdmin)
			r.Get("/admin/stats", adminH.Stats)
			r.Get("/admin/users", adminH.ListUsers)
			r.Post("/admin/users/{id}/ban", adminH.Ban)
			r.Post("/admin/users/{id}/unban", adminH.Unban)
			r.Put("/admin/users/{id}/power", adminH.SetPower)
		})
	})

	return r
}
