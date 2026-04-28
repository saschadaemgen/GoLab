//go:build integration

package model

// Sprint 17 integration tests for the ReadingStore hot path. They
// need a real Postgres instance with the schema migrated up to
// migration 031:
//
//   GOLAB_TEST_DB=postgres://golab:golab-dev@127.0.0.1:5432/golab_test?sslmode=disable \
//     go test -tags integration ./internal/model/...
//
// Each test calls resetReadingTables in setup so the suite is
// repeatable. The reset truncates the four reading-tracker tables
// (reading_events with all its partitions via CASCADE, plus
// user_reading_stats, user_visits, topic_views). It does NOT touch
// users / posts / settings - those stay untouched so other suites
// in the same DB keep working.
//
// What these tests pin:
//
//   TestRecordHeartbeat_BelowThresholdNotCounted - a single 3s post
//     visible-time creates a reading_event with counted_as_read=
//     false; the event row exists but the stats counter stays zero
//     because the threshold has not been crossed.
//   TestRecordHeartbeat_CumulativeCrossesThreshold - two heartbeats
//     each contributing 3s for the same post inside the 5-minute
//     window are merged into one row and flip counted_as_read=true
//     once the running total crosses MinSecondsPerPost (4 by default).
//   TestRecordHeartbeat_TopicViewIdempotent - reporting the same
//     topic id twice across two heartbeats produces exactly one
//     topic_views row; the second insert hits ON CONFLICT DO NOTHING.
//   TestRecordHeartbeat_VisitIdempotent - any number of heartbeats
//     in a single calendar day produces exactly one user_visits row.
//   TestRecordHeartbeat_StatsUpdate - the projection table reflects
//     the source-table state after a heartbeat (active_seconds
//     accumulates, the three count columns are derived from
//     reading_events / topic_views / user_visits).
//   TestGetStats_UnknownUserReturnsZero - GetStats returns a non-nil
//     zero-valued ReadingStats for a user with no recorded activity
//     so the profile template can rely on a non-nil pointer.

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type readingSuite struct {
	pool   *pgxpool.Pool
	store  *ReadingStore
	userID int64
	postID int64
	post2  int64
}

func setupReadingSuite(t *testing.T) *readingSuite {
	t.Helper()
	dsn := os.Getenv("GOLAB_TEST_DB")
	if dsn == "" {
		t.Skip("GOLAB_TEST_DB not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	s := &readingSuite{
		pool:  pool,
		store: &ReadingStore{DB: pool},
	}

	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&s.userID); err != nil {
		t.Skipf("test DB needs at least one user: %v", err)
	}

	// Two posts so cumulative tests can use post 1 and idempotency
	// tests can use post 2 without cross-test contamination beyond
	// the table reset.
	if err := pool.QueryRow(ctx,
		`SELECT id FROM posts ORDER BY id LIMIT 1`).Scan(&s.postID); err != nil {
		t.Skipf("test DB needs at least one post: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM posts WHERE id <> $1 ORDER BY id LIMIT 1`,
		s.postID).Scan(&s.post2); err != nil {
		// Some test DBs only have one seeded post; reuse the same id.
		s.post2 = s.postID
	}

	resetReadingTables(t, pool)
	return s
}

func resetReadingTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// reading_events is partitioned; TRUNCATE on the parent cascades
	// to every partition, which is what we want here.
	if _, err := pool.Exec(ctx,
		`TRUNCATE reading_events, user_reading_stats, user_visits, topic_views
		 RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate reading tables: %v", err)
	}
}

// ============================================================
// reading_events behaviour
// ============================================================

func TestRecordHeartbeat_BelowThresholdNotCounted(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	// 3 seconds < default MinSecondsPerPost (4). The event row gets
	// created so future heartbeats in the 5-minute window can extend
	// it, but counted_as_read stays false.
	err := s.store.RecordHeartbeat(ctx, s.userID, HeartbeatRequest{
		ActiveSeconds: 3,
		VisiblePosts:  []VisiblePost{{PostID: s.postID, SecondsVisible: 3}},
	})
	if err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	var counted bool
	var seconds float64
	err = s.pool.QueryRow(ctx, `
		SELECT counted_as_read, seconds_visible
		FROM reading_events
		WHERE user_id = $1 AND post_id = $2`,
		s.userID, s.postID,
	).Scan(&counted, &seconds)
	if err != nil {
		t.Fatalf("query reading_events: %v", err)
	}
	if counted {
		t.Errorf("counted_as_read = true, want false (3s < 4s threshold)")
	}
	if seconds != 3 {
		t.Errorf("seconds_visible = %v, want 3", seconds)
	}

	// posts_read_count must reflect zero counted-as-read rows.
	stats, err := s.store.GetStats(ctx, s.userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.PostsReadCount != 0 {
		t.Errorf("PostsReadCount = %d, want 0 (under threshold)", stats.PostsReadCount)
	}
}

func TestRecordHeartbeat_CumulativeCrossesThreshold(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	// Two heartbeats of 3s each within the 5-minute window. The
	// upsert merges them into one row; the running total is 6s
	// which crosses the 4s threshold.
	for i := 0; i < 2; i++ {
		err := s.store.RecordHeartbeat(ctx, s.userID, HeartbeatRequest{
			ActiveSeconds: 3,
			VisiblePosts:  []VisiblePost{{PostID: s.postID, SecondsVisible: 3}},
		})
		if err != nil {
			t.Fatalf("RecordHeartbeat #%d: %v", i+1, err)
		}
	}

	var counted bool
	var seconds float64
	var rowCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), MAX(seconds_visible::float8), bool_or(counted_as_read)
		FROM reading_events
		WHERE user_id = $1 AND post_id = $2`,
		s.userID, s.postID,
	).Scan(&rowCount, &seconds, &counted); err != nil {
		t.Fatalf("aggregate reading_events: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("got %d reading_events rows, want 1 (upsert should merge)", rowCount)
	}
	if seconds < 5.99 {
		t.Errorf("merged seconds_visible = %v, want >= 6", seconds)
	}
	if !counted {
		t.Error("counted_as_read = false after crossing threshold")
	}

	stats, err := s.store.GetStats(ctx, s.userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.PostsReadCount != 1 {
		t.Errorf("PostsReadCount = %d, want 1", stats.PostsReadCount)
	}
	if stats.TotalSecondsActive != 6 {
		t.Errorf("TotalSecondsActive = %d, want 6", stats.TotalSecondsActive)
	}
}

// ============================================================
// topic_views idempotency
// ============================================================

func TestRecordHeartbeat_TopicViewIdempotent(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		err := s.store.RecordHeartbeat(ctx, s.userID, HeartbeatRequest{
			TopicsEntered: []int64{s.post2},
		})
		if err != nil {
			t.Fatalf("RecordHeartbeat #%d: %v", i+1, err)
		}
	}

	var rowCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM topic_views WHERE user_id = $1 AND topic_id = $2`,
		s.userID, s.post2,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count topic_views: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("got %d topic_views rows, want 1 (ON CONFLICT DO NOTHING)", rowCount)
	}

	stats, err := s.store.GetStats(ctx, s.userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TopicsEnteredCount != 1 {
		t.Errorf("TopicsEnteredCount = %d, want 1", stats.TopicsEnteredCount)
	}
}

func TestRecordTopicViewBackstop_Idempotent(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s.store.RecordTopicViewBackstop(ctx, s.userID, s.postID)
	}

	var rowCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM topic_views WHERE user_id = $1 AND topic_id = $2`,
		s.userID, s.postID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count topic_views: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("got %d topic_views rows, want 1 after 5 backstop calls", rowCount)
	}
}

// ============================================================
// user_visits idempotency
// ============================================================

func TestRecordHeartbeat_VisitIdempotent(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		err := s.store.RecordHeartbeat(ctx, s.userID, HeartbeatRequest{
			ActiveSeconds: 1,
		})
		if err != nil {
			t.Fatalf("RecordHeartbeat #%d: %v", i+1, err)
		}
	}

	var rowCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_visits WHERE user_id = $1`,
		s.userID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count user_visits: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("got %d user_visits rows, want 1 (one per day)", rowCount)
	}

	stats, err := s.store.GetStats(ctx, s.userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.DistinctDaysVisited != 1 {
		t.Errorf("DistinctDaysVisited = %d, want 1", stats.DistinctDaysVisited)
	}
}

// ============================================================
// stats projection
// ============================================================

func TestRecordHeartbeat_StatsUpdate(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	// One heartbeat with five seconds active, one post above the
	// threshold, one topic view.
	err := s.store.RecordHeartbeat(ctx, s.userID, HeartbeatRequest{
		ActiveSeconds: 5,
		VisiblePosts:  []VisiblePost{{PostID: s.postID, SecondsVisible: 5}},
		TopicsEntered: []int64{s.post2},
	})
	if err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	stats, err := s.store.GetStats(ctx, s.userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalSecondsActive != 5 {
		t.Errorf("TotalSecondsActive = %d, want 5", stats.TotalSecondsActive)
	}
	if stats.PostsReadCount != 1 {
		t.Errorf("PostsReadCount = %d, want 1", stats.PostsReadCount)
	}
	if stats.TopicsEnteredCount != 1 {
		t.Errorf("TopicsEnteredCount = %d, want 1", stats.TopicsEnteredCount)
	}
	if stats.DistinctDaysVisited != 1 {
		t.Errorf("DistinctDaysVisited = %d, want 1", stats.DistinctDaysVisited)
	}
	if stats.LastActiveAt == nil {
		t.Error("LastActiveAt is nil after a heartbeat")
	}
}

// ============================================================
// GetStats edge case
// ============================================================

func TestGetStats_UnknownUserReturnsZero(t *testing.T) {
	s := setupReadingSuite(t)
	ctx := context.Background()

	// Pick an id that is unlikely to ever be a real user.
	stats, err := s.store.GetStats(ctx, 9_999_999)
	if err != nil {
		t.Fatalf("GetStats unknown user: %v", err)
	}
	if stats == nil {
		t.Fatal("GetStats returned nil pointer for unknown user")
	}
	if stats.UserID != 9_999_999 {
		t.Errorf("UserID = %d, want 9999999", stats.UserID)
	}
	if stats.TotalSecondsActive != 0 {
		t.Errorf("TotalSecondsActive = %d, want 0", stats.TotalSecondsActive)
	}
}
