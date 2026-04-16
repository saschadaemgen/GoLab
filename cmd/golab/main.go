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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/config"
	"github.com/saschadaemgen/GoLab/internal/database"
	"github.com/saschadaemgen/GoLab/internal/handler"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

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

	// Markdown renderer
	md := render.NewMarkdown()

	// WebSocket hub
	hub := handler.NewHub(tmpls)
	hubCtx, hubCancel := context.WithCancel(ctx)
	defer hubCancel()
	go hub.Run(hubCtx)

	// Build router
	r := newRouter(cfg, pool, tmpls, md, hub)

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

func newRouter(cfg *config.Config, pool *pgxpool.Pool, tmpls *render.Engine, md *render.Markdown, hub *handler.Hub) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
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
		Markdown: md, Hub: hub, Notifs: notifDispatch,
	}
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

		// Auth (public)
		r.Post("/register", authH.Register)
		r.Post("/login", authH.Login)

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

		// Posts (protected)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/posts", postH.Create)
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
			r.Post("/users/me/avatar", avatarH.Upload)
			r.Delete("/users/me/avatar", avatarH.Remove)
			r.Post("/users/{username}/follow", profileH.Follow)
			r.Delete("/users/{username}/follow", profileH.Unfollow)
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
