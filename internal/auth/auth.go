package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

type SessionStore struct {
	DB *pgxpool.Pool
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SessionStore) Create(ctx context.Context, userID int64) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", fmt.Errorf("generating session id: %w", err)
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour) // 30 days
	_, err = s.DB.Exec(ctx,
		`INSERT INTO sessions (id, user_id, data, expires_at) VALUES ($1, $2, $3, $4)`,
		id, userID, []byte("{}"), expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return id, nil
}

func (s *SessionStore) Find(ctx context.Context, id string) (int64, error) {
	var userID int64
	err := s.DB.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE id = $1 AND expires_at > NOW()`,
		id,
	).Scan(&userID)
	if err != nil {
		return 0, fmt.Errorf("finding session: %w", err)
	}
	return userID, nil
}

func (s *SessionStore) Delete(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteAllForUser removes every session row for a user. Called after
// a password change so every device is forced to log in again with
// the new password.
func (s *SessionStore) DeleteAllForUser(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("deleting user sessions: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at < NOW()`)
	if err != nil {
		return fmt.Errorf("deleting expired sessions: %w", err)
	}
	return nil
}
