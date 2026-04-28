package model

// Reading tracker settings (settings table keys, all configurable
// at runtime via the Site Settings UI when that lands in Sprint 24;
// for v1 the Prinz updates them via direct SQL):
//
// reading_min_seconds_per_post           - Default 4. Minimum seconds
//                                          a post must be visible
//                                          (and the user active) to
//                                          count as "read".
// reading_heartbeat_interval_seconds     - Default 15. How often the
//                                          client sends a heartbeat.
//                                          Server caps per-post
//                                          seconds to this value.
// reading_idle_timeout_seconds           - Default 30. How long
//                                          without mouse / key /
//                                          scroll activity before
//                                          the user is treated as
//                                          idle and heartbeats stop.
// reading_max_active_seconds_per_minute  - Default 60. Server-side
//                                          anti-gaming cap on
//                                          active_seconds per
//                                          heartbeat. Cannot exceed
//                                          wall-clock seconds.
// reading_max_posts_per_heartbeat        - Default 20. Cap on the
//                                          number of visible_posts
//                                          accepted per heartbeat.
//
// The store recomputes user_reading_stats from the source tables on
// every heartbeat. total_seconds_active is incremental; the three
// COUNT columns are derived from reading_events / topic_views /
// user_visits because those tables are the source of truth.
//
// TODO (later sprints):
//   - Per-user opt-out: users.disable_reading_tracker BOOL.
//   - Retention: drop reading_events partitions older than 90 days.
//   - Background reconciler goroutine for drift between counters and
//     source tables (only needed if drift is observed in v1).

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HeartbeatRequest is the JSON the client posts to /api/reading/heartbeat.
// Field semantics:
//
//   - ActiveSeconds is the user's reported active time in this window.
//     The server caps it at ReadingConfig.MaxActiveSecondsPerMinute.
//   - VisiblePosts lists posts that were in the viewport during this
//     window. SecondsVisible is the per-post accumulator since the
//     last heartbeat. The server caps each entry at the heartbeat
//     interval and the slice length at MaxPostsPerHeartbeat.
//   - TopicsEntered are post ids the user opened a detail view for
//     (one entry per topic, the client deduplicates client-side).
type HeartbeatRequest struct {
	ActiveSeconds float64       `json:"active_seconds"`
	VisiblePosts  []VisiblePost `json:"visible_posts"`
	TopicsEntered []int64       `json:"topics_entered"`
}

type VisiblePost struct {
	PostID         int64   `json:"post_id"`
	SecondsVisible float64 `json:"seconds_visible"`
}

// ReadingConfig holds the runtime tunables. LoadConfig fills any
// missing keys with sensible defaults so a fresh deployment without
// settings rows still functions.
type ReadingConfig struct {
	MinSecondsPerPost         float64
	HeartbeatIntervalSeconds  float64
	IdleTimeoutSeconds        float64
	MaxActiveSecondsPerMinute float64
	MaxPostsPerHeartbeat      int
}

// ReadingStats are the four counters the profile page renders.
type ReadingStats struct {
	UserID              int64      `json:"user_id"`
	TotalSecondsActive  int64      `json:"total_seconds_active"`
	PostsReadCount      int        `json:"posts_read_count"`
	TopicsEnteredCount  int        `json:"topics_entered_count"`
	DistinctDaysVisited int        `json:"distinct_days_visited"`
	LastActiveAt        *time.Time `json:"last_active_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type ReadingStore struct {
	DB *pgxpool.Pool
}

// LoadConfig reads all reading_* keys in one query and parses each
// into the typed ReadingConfig. Missing keys keep their default; bad
// values are silently ignored so a typo in the settings table does
// not crash the heartbeat endpoint.
func (s *ReadingStore) LoadConfig(ctx context.Context) (*ReadingConfig, error) {
	cfg := &ReadingConfig{
		MinSecondsPerPost:         4,
		HeartbeatIntervalSeconds:  15,
		IdleTimeoutSeconds:        30,
		MaxActiveSecondsPerMinute: 60,
		MaxPostsPerHeartbeat:      20,
	}

	rows, err := s.DB.Query(ctx,
		`SELECT key, value FROM settings WHERE key LIKE 'reading_%'`)
	if err != nil {
		return cfg, fmt.Errorf("load reading config: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return cfg, fmt.Errorf("load reading config: scan: %w", err)
		}
		switch k {
		case "reading_min_seconds_per_post":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.MinSecondsPerPost = f
			}
		case "reading_heartbeat_interval_seconds":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.HeartbeatIntervalSeconds = f
			}
		case "reading_idle_timeout_seconds":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.IdleTimeoutSeconds = f
			}
		case "reading_max_active_seconds_per_minute":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.MaxActiveSecondsPerMinute = f
			}
		case "reading_max_posts_per_heartbeat":
			if i, err := strconv.Atoi(v); err == nil {
				cfg.MaxPostsPerHeartbeat = i
			}
		}
	}
	if err := rows.Err(); err != nil {
		return cfg, fmt.Errorf("load reading config: rows: %w", err)
	}
	return cfg, nil
}

// RecordHeartbeat persists one client heartbeat in a single
// transaction. Caller is expected to have validated and capped the
// request already (the handler does this).
//
// The 5-minute window in the upsert is the boundary between "still
// the same view" and "user came back later, count fresh seconds":
// if a row for (user, post) exists with last_seen_at within 5
// minutes we extend it; otherwise we insert a new row. This handles
// scroll-back cleanly without pretending the user read for 30
// minutes when they were reading other posts in between.
func (s *ReadingStore) RecordHeartbeat(ctx context.Context, userID int64, req HeartbeatRequest) error {
	if userID <= 0 {
		return fmt.Errorf("heartbeat: invalid user id")
	}

	cfg, err := s.LoadConfig(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat: load config: %w", err)
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, vp := range req.VisiblePosts {
		if vp.PostID <= 0 || vp.SecondsVisible <= 0 {
			continue
		}
		_, err := tx.Exec(ctx, `
			WITH existing AS (
				SELECT id, last_seen_at, seconds_visible
				FROM reading_events
				WHERE user_id = $1
				  AND post_id = $2
				  AND last_seen_at > NOW() - INTERVAL '5 minutes'
				ORDER BY last_seen_at DESC
				LIMIT 1
			), updated AS (
				UPDATE reading_events re
				SET seconds_visible = re.seconds_visible + $3,
				    last_seen_at    = NOW(),
				    counted_as_read = (re.seconds_visible + $3) >= $4
				FROM existing
				WHERE re.id = existing.id
				  AND re.last_seen_at = existing.last_seen_at
				RETURNING re.id
			)
			INSERT INTO reading_events (user_id, post_id, seconds_visible, counted_as_read)
			SELECT $1, $2, $3, $3 >= $4
			WHERE NOT EXISTS (SELECT 1 FROM updated)`,
			userID, vp.PostID, vp.SecondsVisible, cfg.MinSecondsPerPost)
		if err != nil {
			return fmt.Errorf("heartbeat: upsert event: %w", err)
		}
	}

	for _, tid := range req.TopicsEntered {
		if tid <= 0 {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO topic_views (user_id, topic_id) VALUES ($1, $2)
			ON CONFLICT (user_id, topic_id) DO NOTHING`,
			userID, tid)
		if err != nil {
			return fmt.Errorf("heartbeat: topic view: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_visits (user_id, visit_date) VALUES ($1, CURRENT_DATE)
		ON CONFLICT (user_id, visit_date) DO NOTHING`,
		userID); err != nil {
		return fmt.Errorf("heartbeat: visit: %w", err)
	}

	// Stats projection: total_seconds_active is incremental, the three
	// count columns are derived from the source tables we just wrote
	// to so they always reflect the latest truth. last_active_at gets
	// stamped to NOW() on every heartbeat regardless of whether any
	// other column changed.
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_reading_stats (
			user_id, total_seconds_active, posts_read_count,
			topics_entered_count, distinct_days_visited,
			last_active_at, updated_at
		) VALUES (
			$1,
			$2,
			(SELECT COUNT(DISTINCT post_id) FROM reading_events WHERE user_id = $1 AND counted_as_read = TRUE),
			(SELECT COUNT(*) FROM topic_views WHERE user_id = $1),
			(SELECT COUNT(*) FROM user_visits WHERE user_id = $1),
			NOW(),
			NOW()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			total_seconds_active  = user_reading_stats.total_seconds_active + EXCLUDED.total_seconds_active,
			posts_read_count      = EXCLUDED.posts_read_count,
			topics_entered_count  = EXCLUDED.topics_entered_count,
			distinct_days_visited = EXCLUDED.distinct_days_visited,
			last_active_at        = NOW(),
			updated_at            = NOW()`,
		userID, int64(req.ActiveSeconds)); err != nil {
		return fmt.Errorf("heartbeat: stats: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("heartbeat: commit: %w", err)
	}
	return nil
}

// RecordTopicViewBackstop is a fire-and-forget safety net for page
// handlers that serve a single-topic view. The frontend tracker
// reports topics it sees, but a tab-restore or a JS-disabled visit
// would miss the view; the backstop records it server-side from the
// page render path. Errors are logged and swallowed so a failed
// insert never breaks the page.
func (s *ReadingStore) RecordTopicViewBackstop(ctx context.Context, userID, topicID int64) {
	if userID <= 0 || topicID <= 0 {
		return
	}
	if _, err := s.DB.Exec(ctx, `
		INSERT INTO topic_views (user_id, topic_id) VALUES ($1, $2)
		ON CONFLICT (user_id, topic_id) DO NOTHING`,
		userID, topicID); err != nil {
		slog.Warn("topic view backstop",
			"user_id", userID, "topic_id", topicID, "err", err)
	}
}

// GetStats returns the four counters for a user. A user with no
// recorded activity gets a zero-valued ReadingStats back rather
// than an error so profile rendering can always count on a non-nil
// pointer.
func (s *ReadingStore) GetStats(ctx context.Context, userID int64) (*ReadingStats, error) {
	st := &ReadingStats{UserID: userID}
	err := s.DB.QueryRow(ctx, `
		SELECT total_seconds_active, posts_read_count,
		       topics_entered_count, distinct_days_visited,
		       last_active_at, updated_at
		FROM user_reading_stats
		WHERE user_id = $1`,
		userID,
	).Scan(
		&st.TotalSecondsActive, &st.PostsReadCount,
		&st.TopicsEnteredCount, &st.DistinctDaysVisited,
		&st.LastActiveAt, &st.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get reading stats: %w", err)
	}
	return st, nil
}
