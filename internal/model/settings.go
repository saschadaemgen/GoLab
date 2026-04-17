package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore is a tiny key/value cache over the settings table.
// Values are cached in-process because they change rarely (admin
// toggles only) and we read them on every register/post/etc.
// On write we refresh the cache entry; there's no pub/sub across
// server instances yet - we only run one instance.
type SettingsStore struct {
	DB    *pgxpool.Pool
	mu    sync.RWMutex
	cache map[string]string
}

// Get returns the value for a key, falling back to "" if the key
// doesn't exist. Caches the value after first read.
func (s *SettingsStore) Get(ctx context.Context, key string) string {
	s.mu.RLock()
	if s.cache != nil {
		if v, ok := s.cache[key]; ok {
			s.mu.RUnlock()
			return v
		}
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[string]string)
	}
	// Double-check after acquiring write lock.
	if v, ok := s.cache[key]; ok {
		return v
	}
	var v string
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if err == pgx.ErrNoRows {
		s.cache[key] = ""
		return ""
	}
	if err != nil {
		return "" // soft fail - avoid crashing callers
	}
	s.cache[key] = v
	return v
}

// GetBool is a convenience wrapper for string-encoded booleans.
func (s *SettingsStore) GetBool(ctx context.Context, key string) bool {
	return s.Get(ctx, key) == "true"
}

// Set writes a value and refreshes the cache.
func (s *SettingsStore) Set(ctx context.Context, key, value string) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = make(map[string]string)
	}
	s.cache[key] = value
	s.mu.Unlock()
	return nil
}

// silence unused import warning when time is only referenced inline
var _ = time.Now
