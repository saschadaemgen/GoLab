package model

// Sprint 17 reflection guards for the reading-tracker types. These
// run on every `go test ./...` (no build tag) so a future refactor
// that accidentally renames a field or drops a method gets caught
// before integration tests need a real DB to fail.

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReadingStore_HasDBField pins the store's wiring contract:
// main.go constructs &ReadingStore{DB: pool} and the rest of the
// package counts on s.DB being a *pgxpool.Pool.
func TestReadingStore_HasDBField(t *testing.T) {
	tt := reflect.TypeOf(ReadingStore{})
	f, ok := tt.FieldByName("DB")
	if !ok {
		t.Fatal("ReadingStore is missing the DB field")
	}
	want := reflect.TypeOf((*pgxpool.Pool)(nil))
	if f.Type != want {
		t.Errorf("ReadingStore.DB type = %s, want %s", f.Type, want)
	}
}

// TestHeartbeatRequest_Shape locks the JSON contract the handler
// decodes and the client sends. Field names and JSON tags must match
// the briefing exactly so the existing client keeps working.
func TestHeartbeatRequest_Shape(t *testing.T) {
	tt := reflect.TypeOf(HeartbeatRequest{})
	cases := map[string]struct {
		kind reflect.Kind
		tag  string
	}{
		"ActiveSeconds": {reflect.Float64, "active_seconds"},
		"VisiblePosts":  {reflect.Slice, "visible_posts"},
		"TopicsEntered": {reflect.Slice, "topics_entered"},
	}
	for name, want := range cases {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("HeartbeatRequest is missing %s", name)
			continue
		}
		if f.Type.Kind() != want.kind {
			t.Errorf("HeartbeatRequest.%s kind = %s, want %s", name, f.Type.Kind(), want.kind)
		}
		if got := f.Tag.Get("json"); got != want.tag {
			t.Errorf("HeartbeatRequest.%s json tag = %q, want %q", name, got, want.tag)
		}
	}
}

// TestVisiblePost_Shape locks the per-post element of the visible_posts
// slice. PostID is int64 to match the rest of the codebase; the client
// posts numeric IDs as JSON numbers.
func TestVisiblePost_Shape(t *testing.T) {
	tt := reflect.TypeOf(VisiblePost{})
	cases := map[string]struct {
		kind reflect.Kind
		tag  string
	}{
		"PostID":         {reflect.Int64, "post_id"},
		"SecondsVisible": {reflect.Float64, "seconds_visible"},
	}
	for name, want := range cases {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("VisiblePost is missing %s", name)
			continue
		}
		if f.Type.Kind() != want.kind {
			t.Errorf("VisiblePost.%s kind = %s, want %s", name, f.Type.Kind(), want.kind)
		}
		if got := f.Tag.Get("json"); got != want.tag {
			t.Errorf("VisiblePost.%s json tag = %q, want %q", name, got, want.tag)
		}
	}
}

// TestReadingConfig_HasFields keeps the typed view of the settings
// rows in lockstep with the migration's INSERT keys. Any of these
// missing means a settings row would not have anywhere to land.
func TestReadingConfig_HasFields(t *testing.T) {
	tt := reflect.TypeOf(ReadingConfig{})
	required := map[string]reflect.Kind{
		"MinSecondsPerPost":         reflect.Float64,
		"HeartbeatIntervalSeconds":  reflect.Float64,
		"IdleTimeoutSeconds":        reflect.Float64,
		"MaxActiveSecondsPerMinute": reflect.Float64,
		"MaxPostsPerHeartbeat":      reflect.Int,
	}
	for name, kind := range required {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("ReadingConfig is missing %s", name)
			continue
		}
		if f.Type.Kind() != kind {
			t.Errorf("ReadingConfig.%s kind = %s, want %s", name, f.Type.Kind(), kind)
		}
	}
}

// TestReadingStats_HasFields locks the four counters the profile page
// consumes plus the two timestamp columns the Trust Level engine
// (Sprint 19) will look at.
func TestReadingStats_HasFields(t *testing.T) {
	tt := reflect.TypeOf(ReadingStats{})
	required := []string{
		"UserID",
		"TotalSecondsActive",
		"PostsReadCount",
		"TopicsEnteredCount",
		"DistinctDaysVisited",
		"LastActiveAt",
		"UpdatedAt",
	}
	for _, name := range required {
		if _, ok := tt.FieldByName(name); !ok {
			t.Errorf("ReadingStats is missing %s", name)
		}
	}
}

// TestReadingStore_HasMethods guards the four public methods main.go
// and the page handlers call. A future refactor that renames any of
// them would break the build downstream; this test catches it inside
// the package.
func TestReadingStore_HasMethods(t *testing.T) {
	tt := reflect.TypeOf(&ReadingStore{})
	for _, m := range []string{
		"LoadConfig",
		"RecordHeartbeat",
		"RecordTopicViewBackstop",
		"GetStats",
	} {
		if _, ok := tt.MethodByName(m); !ok {
			t.Errorf("ReadingStore is missing method %s", m)
		}
	}
}

// TestRecordTopicViewBackstop_FireAndForget proves the backstop's
// sentinel-input behaviour: zero or negative ids return early
// without touching the (nil) pool. This guards against accidentally
// turning the helper into something that would NPE when called from
// a handler that has not yet validated its inputs.
func TestRecordTopicViewBackstop_FireAndForget(t *testing.T) {
	s := &ReadingStore{DB: nil}
	// Both invalid - method must return without dereferencing DB.
	s.RecordTopicViewBackstop(context.Background(), 0, 0)
	s.RecordTopicViewBackstop(context.Background(), -1, 5)
	s.RecordTopicViewBackstop(context.Background(), 5, -1)
	// If we got here without a panic, the early-return guards work.
}
