package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID               int64   `json:"id"`
	Username         string  `json:"username"`
	PasswordHash     string  `json:"-"`
	DisplayName      string  `json:"display_name"`
	Bio              string  `json:"bio"`
	AvatarURL        string  `json:"avatar_url"`
	PowerLevel       int     `json:"power_level"`
	DIDKey           *string `json:"did_key,omitempty"`
	CertFingerprint  *string `json:"cert_fingerprint,omitempty"`
	HardwareVerified bool    `json:"hardware_verified"`
	Banned           bool    `json:"-"`
	// Sprint 12 moderation: "active", "pending", "rejected". Existing
	// users are "active" by migration default.
	Status     string     `json:"status"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy *int64     `json:"reviewed_by,omitempty"`
	// Sprint X application fields. Filled at /register, displayed in
	// the admin pending-users panel, kept on the row after approval
	// as moderation history. omitempty on the optional pair so the
	// API response stays small for users that left them blank.
	ExternalLinks         string `json:"external_links,omitempty"`
	EcosystemConnection   string `json:"ecosystem_connection,omitempty"`
	CommunityContribution string `json:"community_contribution,omitempty"`
	CurrentFocus          string `json:"current_focus,omitempty"`
	ApplicationNotes      string `json:"application_notes,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
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

// UserCreateParams bundles the fields required to register a new
// user. Sprint X: replaces the previous (username, email,
// passwordHash, status) positional signature. The application
// fields are stored verbatim (no markdown rendering, displayed as
// plain text in the admin review panel) and validated at the
// handler layer. Named with the User prefix to avoid collision
// with model.CreateParams used by the post store.
type UserCreateParams struct {
	Username              string
	PasswordHash          string
	Status                string
	ExternalLinks         string
	EcosystemConnection   string
	CommunityContribution string
	CurrentFocus          string
	ApplicationNotes      string
}

// Create inserts a new user. The `Status` field controls the
// Sprint 12 moderation workflow: "active" means the user can post
// immediately, "pending" means they need admin approval first. The
// very first user (id=1) is always promoted to Owner AND active
// regardless of status, so the platform isn't DOA from its own
// approval gate.
func (s *UserStore) Create(ctx context.Context, p UserCreateParams) (*User, error) {
	var userCount int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return nil, fmt.Errorf("counting users: %w", err)
	}
	powerLevel := 10
	if userCount == 0 {
		powerLevel = 100
		p.Status = UserStatusActive // first user bootstraps the platform
	}
	if p.Status == "" {
		p.Status = UserStatusActive
	}

	var id int64
	if err := s.DB.QueryRow(ctx,
		`INSERT INTO users (
			username, password_hash, display_name, power_level, status,
			external_links, ecosystem_connection, community_contribution,
			current_focus, application_notes
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		p.Username, p.PasswordHash, p.Username, powerLevel, p.Status,
		p.ExternalLinks, p.EcosystemConnection, p.CommunityContribution,
		p.CurrentFocus, p.ApplicationNotes,
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

// userColumns is the canonical SELECT list every read query uses.
// Centralised so adding/removing fields stays consistent. Order MUST
// match the Scan order in scanUser below.
const userColumns = `id, username, password_hash, display_name, bio, avatar_url,
	power_level, did_key, cert_fingerprint, hardware_verified, banned,
	status, reviewed_at, reviewed_by,
	external_links, ecosystem_connection, community_contribution,
	current_focus, application_notes,
	created_at, updated_at`

// scanUser reads one user row in the order userColumns lays out.
// Used by every Find* and List* method so adding a column means
// touching one place.
func scanUser(row pgx.Row, u *User) error {
	return row.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.AvatarURL, &u.PowerLevel, &u.DIDKey, &u.CertFingerprint,
		&u.HardwareVerified, &u.Banned,
		&u.Status, &u.ReviewedAt, &u.ReviewedBy,
		&u.ExternalLinks, &u.EcosystemConnection, &u.CommunityContribution,
		&u.CurrentFocus, &u.ApplicationNotes,
		&u.CreatedAt, &u.UpdatedAt,
	)
}

// ListPending returns all users in "pending" status for the admin
// approval queue.
func (s *UserStore) ListPending(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx,
		`SELECT `+userColumns+`
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
		if err := scanUser(rows, &u); err != nil {
			return nil, fmt.Errorf("scan pending user: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

// Promote sets a user's power_level. Sprint X.2: extracted from the
// inline UPDATE that lived in Create's belt-and-suspenders branch
// so the registration handler can call it explicitly for the first
// user without re-implementing the SQL. Used as defence in depth
// alongside the userCount==0 branch in Create; even if Create's
// branch ever fails to fire (count race, transaction wrinkle), the
// handler-level promotion still lifts the bootstrapping admin to
// Owner level.
func (s *UserStore) Promote(ctx context.Context, userID int64, level int) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE users SET power_level = $2, updated_at = NOW() WHERE id = $1`,
		userID, level,
	)
	if err != nil {
		return fmt.Errorf("promoting user: %w", err)
	}
	return nil
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
		`SELECT `+userColumns+`
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
		if err := scanUser(rows, &u); err != nil {
			return nil, fmt.Errorf("scan admin: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *UserStore) FindByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := scanUser(s.DB.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = $1`,
		username,
	), u)
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
	err := scanUser(s.DB.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`,
		id,
	), u)
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

// UpdatePassword swaps the user's bcrypt hash. Callers should hash
// before calling; the hash column stores the raw bcrypt string.
func (s *UserStore) UpdatePassword(ctx context.Context, id int64, newHash string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`,
		id, newHash,
	)
	if err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	return nil
}

// UpdateUsername changes the handle. The caller must validate format
// and uniqueness (case-insensitive) BEFORE calling; the DB's UNIQUE
// index on username is case-sensitive so callers also use
// UsernameExists as a gate.
func (s *UserStore) UpdateUsername(ctx context.Context, id int64, newUsername string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE users SET username = $2, updated_at = NOW() WHERE id = $1`,
		id, newUsername,
	)
	if err != nil {
		return fmt.Errorf("updating username: %w", err)
	}
	return nil
}

// UsernameExists reports whether any user already uses this handle
// (case-insensitive). Used as a gate before UpdateUsername to give
// users a clean "already taken" error instead of a SQL unique
// violation.
func (s *UserStore) UsernameExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username) = LOWER($1))`,
		username,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking username: %w", err)
	}
	return exists, nil
}
