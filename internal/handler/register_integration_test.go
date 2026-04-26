//go:build integration

package handler

// Sprint X.2 integration tests for the first-user bootstrap and the
// require_approval gate that follow it. These tests need a real
// PostgreSQL instance with the schema migrated up to migration 026:
//
//   GOLAB_TEST_DB=postgres://golab:golab-dev@127.0.0.1:5432/golab_test?sslmode=disable \
//   go test -tags integration ./internal/handler/...
//
// The test exits early with t.Skip when GOLAB_TEST_DB is unset, so a
// developer running `go test ./...` without the tag and without a DB
// gets the existing pure-function suite. The file is build-tag-gated
// so the integration code does not pull pgxpool into the default
// build's test binary.
//
// What the tests pin:
//
//   TestRegister_FirstUserGetsOwner      first user -> id=1, power_level=100,
//                                        status=active regardless of
//                                        require_approval setting
//   TestRegister_SecondUserGetsDefault   user 2 -> power_level=10,
//                                        status=pending when require_approval
//                                        is on
//
// These cases lock the contract Sprint X.2 added handler-level (the
// model's Create() COUNT branch and the handler's id==1 promote +
// auto-approve). Both layers run today; if a future refactor drops
// either, these tests catch it.
//
// TODO(sprint X.3 or later): wire a t.Cleanup() that truncates the
// users + sessions tables so the test suite is repeatable. For now
// the tests assume a fresh DB and run only on demand.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

func setupTestHandler(t *testing.T) (*AuthHandler, *pgxpool.Pool) {
	dsn := os.Getenv("GOLAB_TEST_DB")
	if dsn == "" {
		t.Skip("GOLAB_TEST_DB not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	users := &model.UserStore{DB: pool}
	settings := &model.SettingsStore{DB: pool}
	sessions := &auth.SessionStore{DB: pool}
	h := &AuthHandler{
		Users:    users,
		Sessions: sessions,
		Settings: settings,
	}
	return h, pool
}

func postRegister(t *testing.T, h *AuthHandler, username, password string) *http.Response {
	t.Helper()
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	form.Set("ecosystem_connection", strings.Repeat("I am building hardware in the SimpleGo ecosystem. ", 1))
	form.Set("community_contribution", strings.Repeat("I will contribute hardware-side review and notes. ", 1))
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Register(rec, req)
	return rec.Result()
}

func TestRegister_FirstUserGetsOwner(t *testing.T) {
	h, pool := setupTestHandler(t)
	defer pool.Close()
	// Truncation belongs in t.Cleanup; for now the test assumes a
	// fresh DB. Calling against a populated DB will skip the
	// bootstrap branch and the assertions below will fail loudly.

	resp := postRegister(t, h, "ownertest", "supersecretpw")
	if resp.StatusCode >= 400 {
		t.Fatalf("register first user returned %d", resp.StatusCode)
	}

	user, err := h.Users.FindByUsername(context.Background(), "ownertest")
	if err != nil || user == nil {
		t.Fatalf("first user not in DB: err=%v user=%v", err, user)
	}
	if user.ID != 1 {
		t.Errorf("first user id = %d, want 1", user.ID)
	}
	if user.PowerLevel != 100 {
		t.Errorf("first user power_level = %d, want 100", user.PowerLevel)
	}
	if user.Status != model.UserStatusActive {
		t.Errorf("first user status = %q, want %q", user.Status, model.UserStatusActive)
	}
}

func TestRegister_SecondUserGetsDefault(t *testing.T) {
	h, pool := setupTestHandler(t)
	defer pool.Close()

	if resp := postRegister(t, h, "ownertest", "supersecretpw"); resp.StatusCode >= 400 {
		t.Fatalf("register first user returned %d", resp.StatusCode)
	}
	if resp := postRegister(t, h, "applicant", "anothersecret"); resp.StatusCode >= 400 {
		t.Fatalf("register second user returned %d", resp.StatusCode)
	}

	user, err := h.Users.FindByUsername(context.Background(), "applicant")
	if err != nil || user == nil {
		t.Fatalf("second user not in DB: err=%v user=%v", err, user)
	}
	if user.PowerLevel != 10 {
		t.Errorf("second user power_level = %d, want 10", user.PowerLevel)
	}
	if user.Status != model.UserStatusPending {
		t.Errorf("second user status = %q, want %q", user.Status, model.UserStatusPending)
	}
}

// Sprint Y.4 username-available DB-dependent paths. The pure-
// function tests in auth_test.go cover invalid-format and
// reserved-list rejections; the two cases below need a real
// users table to exercise the UsernameExists call.

func TestUsernameAvailable_AcceptsAvailable(t *testing.T) {
	h, pool := setupTestHandler(t)
	defer pool.Close()

	req := httptest.NewRequest("GET",
		"/api/auth/username-available?u=brandnewhandle", nil)
	rec := httptest.NewRecorder()
	h.CheckUsernameAvailable(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Available {
		t.Errorf("Available = false (reason=%q), want true", body.Reason)
	}
}

func TestUsernameAvailable_RejectsTaken(t *testing.T) {
	h, pool := setupTestHandler(t)
	defer pool.Close()

	if resp := postRegister(t, h, "takenname", "supersecretpw"); resp.StatusCode >= 400 {
		t.Fatalf("seed register returned %d", resp.StatusCode)
	}

	req := httptest.NewRequest("GET",
		"/api/auth/username-available?u=takenname", nil)
	rec := httptest.NewRecorder()
	h.CheckUsernameAvailable(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Available {
		t.Errorf("Available = true, want false (reason should be 'taken')")
	}
	if body.Reason != "taken" {
		t.Errorf("Reason = %q, want %q", body.Reason, "taken")
	}
}
