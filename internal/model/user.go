package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID               int64      `json:"id"`
	Username         string     `json:"username"`
	Email            string     `json:"email,omitempty"`
	PasswordHash     string     `json:"-"`
	DisplayName      string     `json:"display_name"`
	Bio              string     `json:"bio"`
	AvatarURL        string     `json:"avatar_url"`
	PowerLevel       int        `json:"power_level"`
	DIDKey           *string    `json:"did_key,omitempty"`
	CertFingerprint  *string    `json:"cert_fingerprint,omitempty"`
	HardwareVerified bool       `json:"hardware_verified"`
	Banned           bool       `json:"-"`
	// Sprint 12 moderation: "active", "pending", "rejected". Existing
	// users are "active" by migration default.
	Status     string     `json:"status"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy *int64     `json:"reviewed_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Status constants
const (
	UserStatusActive   = "active"
	UserStatusPending  = "pending"
	UserStatusRejected = "rejected"
)

type UserStore struct {
	DB *pgxpool.Pool
}

// Create inserts a new user. The `status` parameter controls the
// Sprint 12 moderation workflow: "active" means the user can post
// immediately, "pending" means they need admin approval first. The
// very first user (id=1) is always promoted to Owner AND active
// regardless of status, so the platform isn't DOA from its own
// approval gate.
func (s *UserStore) Create(ctx context.Context, username, email, passwordHash, status string) (*User, error) {
	var userCount int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return nil, fmt.Errorf("counting users: %w", err)
	}
	powerLevel := 10
	if userCount == 0 {
		powerLevel = 100
		status = UserStatusActive // first user bootstraps the platform
	}
	if status == "" {
		status = UserStatusActive
	}

	var id int64
	if err := s.DB.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name, power_level, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		username, email, passwordHash, username, powerLevel, status,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	// Belt-and-suspenders: promote id=1 if the migration hadn't landed.
	if id == 1 {
		_, _ = s.DB.Exec(ctx,
			`UPDATE users SET power_level = 100, status = 'active' WHERE id = 1`)
	}

	return s.FindByID(ctx, id)
}

// ListPending returns all users in "pending" status for the admin
// approval queue.
func (s *UserStore) ListPending(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned,
		        status, reviewed_at, reviewed_by, created_at, updated_at
		 FROM users
		 WHERE status = 'pending'
		 ORDER BY created_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing pending users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
			&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
			&u.HardwareVerified, &u.Banned,
			&u.Status, &u.ReviewedAt, &u.ReviewedBy,
			&u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending user: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

// SetStatus updates a user's moderation state and records who made
// the decision and when.
func (s *UserStore) SetStatus(ctx context.Context, userID int64, status string, reviewerID int64) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE users SET status = $2, reviewed_at = NOW(), reviewed_by = $3, updated_at = NOW()
		 WHERE id = $1`,
		userID, status, reviewerID)
	if err != nil {
		return fmt.Errorf("setting user status: %w", err)
	}
	return nil
}

// ListAdmins returns every user with power_level >= 75. Used by the
// registration handler to notify admins about new pending users.
func (s *UserStore) ListAdmins(ctx context.Context) ([]User, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned,
		        status, reviewed_at, reviewed_by, created_at, updated_at
		 FROM users
		 WHERE power_level >= 75 AND status = 'active'
		 ORDER BY power_level DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing admins: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
			&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
			&u.HardwareVerified, &u.Banned,
			&u.Status, &u.ReviewedAt, &u.ReviewedBy,
			&u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *UserStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, email, password_hash, display_name, bio, avatar_url,
		        power_level, did_key, cert_fingerprint, hardware_verified, banned,
		        status, reviewed_at, reviewed_by, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned,
		&u.Status, &u.ReviewedAt, &u.ReviewedBy,
		&u.CreatedAt, &u.UpdatedAt,
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
		        power_level, did_key, cert_fingerprint, hardware_verified, banned,
		        status, reviewed_at, reviewed_by, created_at, updated_at
		 FROM users WHERE username = $1`,
		username,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned,
		&u.Status, &u.ReviewedAt, &u.ReviewedBy,
		&u.CreatedAt, &u.UpdatedAt,
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
		        power_level, did_key, cert_fingerprint, hardware_verified, banned,
		        status, reviewed_at, reviewed_by, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned,
		&u.Status, &u.ReviewedAt, &u.ReviewedBy,
		&u.CreatedAt, &u.UpdatedAt,
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
