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

	// Build router
	r := newRouter(cfg, pool)

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

func newRouter(cfg *config.Config, pool *pgxpool.Pool) *chi.Mux {
	r := chi.NewRouter()

	// Middleware
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
	sessions := &auth.SessionStore{DB: pool}

	// Auth middleware
	requireAuth := auth.RequireAuth(sessions, users)

	// Handlers
	home := &handler.HomeHandler{DB: pool}
	authH := &handler.AuthHandler{
		Users:    users,
		Sessions: sessions,
		Secure:   !cfg.IsDev(),
	}
	channelH := &handler.ChannelHandler{
		Channels: channels,
		Users:    users,
	}
	postH := &handler.PostHandler{
		Posts:     posts,
		Channels:  channels,
		Reactions: reactions,
	}
	feedH := &handler.FeedHandler{
		Posts: posts,
	}
	profileH := &handler.ProfileHandler{
		Users:   users,
		Posts:   posts,
		Follows: follows,
	}

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
			r.Post("/users/{username}/follow", profileH.Follow)
			r.Delete("/users/{username}/follow", profileH.Unfollow)
		})
	})

	return r
}
