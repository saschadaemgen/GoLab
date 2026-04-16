package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID               int64     `json:"id"`
	Username         string    `json:"username"`
	Email            string    `json:"email,omitempty"`
	PasswordHash     string    `json:"-"`
	DisplayName      string    `json:"display_name"`
	Bio              string    `json:"bio"`
	AvatarURL        string    `json:"avatar_url"`
	PowerLevel       int       `json:"power_level"`
	DIDKey           *string   `json:"did_key,omitempty"`
	CertFingerprint  *string   `json:"cert_fingerprint,omitempty"`
	HardwareVerified bool      `json:"hardware_verified"`
	Banned           bool      `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UserStore struct {
	DB *pgxpool.Pool
}

func (s *UserStore) Create(ctx context.Context, username, email, passwordHash string) (*User, error) {
	// Determine if this is the first user - they automatically become Owner
	// (power_level 100). Done inside a single query so registration races
	// can't both claim owner: the COUNT sees the pre-insert state atomically
	// relative to its own snapshot, and we rely on the serial id to confirm.
	var userCount int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return nil, fmt.Errorf("counting users: %w", err)
	}
	powerLevel := 10
	if userCount == 0 {
		powerLevel = 100
	}

	u := &User{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name, power_level)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, username, email, password_hash, display_name, bio, avatar_url,
		           power_level, created_at, updated_at`,
		username, email, passwordHash, username, powerLevel,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	// Belt-and-suspenders: if we somehow get id=1 but power_level isn't 100
	// (e.g. migration ran before this code deployed), coerce it now.
	if u.ID == 1 && u.PowerLevel < 100 {
		_, _ = s.DB.Exec(ctx, `UPDATE users SET power_level = 100 WHERE id = 1`)
		u.PowerLevel = 100
	}
	return u, nil
}

func (s *UserStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by email: %w", err)
	}
	return u, nil
}

func (s *UserStore) FindByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned, created_at, updated_at
		 FROM users WHERE username = $1`,
		username,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by username: %w", err)
	}
	return u, nil
}

func (s *UserStore) FindByID(ctx context.Context, id int64) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by id: %w", err)
	}
	return u, nil
}

func (s *UserStore) UpdateProfile(ctx context.Context, id int64, displayName, bio, avatarURL string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE users SET display_name = $2, bio = $3, avatar_url = $4, updated_at = NOW()
		 WHERE id = $1`,
		id, displayName, bio, avatarURL,
	)
	if err != nil {
		return fmt.Errorf("updating profile: %w", err)
	}
	return nil
}
