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
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UserStore struct {
	DB *pgxpool.Pool
}

func (s *UserStore) Create(ctx context.Context, username, email, passwordHash string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, display_name, bio, avatar_url,
		           power_level, created_at, updated_at`,
		username, email, passwordHash, username,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

func (s *UserStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.CreatedAt, &u.UpdatedAt,
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
		        power_level, did_key, cert_fingerprint, hardware_verified, created_at, updated_at
		 FROM users WHERE username = $1`,
		username,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.CreatedAt, &u.UpdatedAt,
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
		        power_level, did_key, cert_fingerprint, hardware_verified, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.CreatedAt, &u.UpdatedAt,
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
