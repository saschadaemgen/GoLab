package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Project member roles. Owner is the project's creator (or transferred
// successor); contributors can write posts and edit docs; viewers can
// see members-only / hidden projects but cannot mutate.
const (
	ProjectRoleOwner       = "owner"
	ProjectRoleContributor = "contributor"
	ProjectRoleViewer      = "viewer"
)

var validProjectRoles = map[string]struct{}{
	ProjectRoleOwner:       {},
	ProjectRoleContributor: {},
	ProjectRoleViewer:      {},
}

// IsValidProjectRole accepts the three role names. Used by handlers
// before they try to insert/update so invalid input gets a 400 instead
// of a CHECK constraint violation.
func IsValidProjectRole(r string) bool {
	_, ok := validProjectRoles[r]
	return ok
}

type ProjectMember struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	UserID    int64     `json:"user_id"`
	Role      string    `json:"role"`
	InvitedBy *int64    `json:"invited_by,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`

	// Joined fields (not always populated). The list endpoint fills
	// these so the UI doesn't need a second roundtrip per member.
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

type ProjectMemberStore struct {
	DB *pgxpool.Pool
}

var (
	ErrMemberNotFound      = errors.New("project member not found")
	ErrMemberAlreadyExists = errors.New("user is already a project member")
	ErrCannotRemoveOwner   = errors.New("cannot remove project owner")
	ErrCannotAssignOwner   = errors.New("ownership transfer is a separate flow")
	ErrInvalidRole         = errors.New("invalid project role")
)

// Add inserts a project_members row. Returns ErrMemberAlreadyExists
// when (project_id, user_id) already exists. Owner rows are normally
// inserted by ProjectStore.Create, not by Add - the role parameter
// stays free here only so admin tooling can fix data drift.
func (s *ProjectMemberStore) Add(ctx context.Context, projectID, userID int64, role string, invitedBy int64) error {
	if !IsValidProjectRole(role) {
		return ErrInvalidRole
	}
	var inv any = invitedBy
	if invitedBy == 0 {
		inv = nil
	}

	var id int64
	err := s.DB.QueryRow(ctx, `
		INSERT INTO project_members (project_id, user_id, role, invited_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, user_id) DO NOTHING
		RETURNING id`,
		projectID, userID, role, inv,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return ErrMemberAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("add project member: %w", err)
	}
	return nil
}

// Remove deletes a non-owner project_members row. Owner removal is
// blocked: ownership has to go through a transfer flow (16c) so the
// projects.owner_id denormalisation stays consistent.
func (s *ProjectMemberStore) Remove(ctx context.Context, projectID, userID int64) error {
	tag, err := s.DB.Exec(ctx, `
		DELETE FROM project_members
		 WHERE project_id = $1 AND user_id = $2 AND role <> 'owner'`,
		projectID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove project member: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var role string
	err = s.DB.QueryRow(ctx,
		`SELECT role FROM project_members
		 WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&role)
	if err == pgx.ErrNoRows {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("remove project member: post-check: %w", err)
	}
	if role == ProjectRoleOwner {
		return ErrCannotRemoveOwner
	}
	// Should be unreachable: the DELETE above would have hit it.
	return ErrMemberNotFound
}

func (s *ProjectMemberStore) GetRole(ctx context.Context, projectID, userID int64) (string, error) {
	var role string
	err := s.DB.QueryRow(ctx,
		`SELECT role FROM project_members
		 WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&role)
	if err == pgx.ErrNoRows {
		return "", ErrMemberNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get project member role: %w", err)
	}
	return role, nil
}

// UpdateRole changes a member's role. Refuses to set role=owner
// (ownership transfer must update both project_members.role and
// projects.owner_id together) and refuses to overwrite an existing
// owner row.
func (s *ProjectMemberStore) UpdateRole(ctx context.Context, projectID, userID int64, role string) error {
	if !IsValidProjectRole(role) {
		return ErrInvalidRole
	}
	if role == ProjectRoleOwner {
		return ErrCannotAssignOwner
	}
	tag, err := s.DB.Exec(ctx, `
		UPDATE project_members
		   SET role = $3
		 WHERE project_id = $1 AND user_id = $2 AND role <> 'owner'`,
		projectID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("update project member role: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// Either the row is absent or it is the owner row. Disambiguate
	// for the handler.
	var existingRole string
	err = s.DB.QueryRow(ctx,
		`SELECT role FROM project_members
		 WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&existingRole)
	if err == pgx.ErrNoRows {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("update project member role: post-check: %w", err)
	}
	if existingRole == ProjectRoleOwner {
		return ErrCannotAssignOwner
	}
	return ErrMemberNotFound
}

// IsMember returns true when the user has any project_members row for
// the given project. Used by the read-side visibility check on hidden
// and members-only projects.
func (s *ProjectMemberStore) IsMember(ctx context.Context, projectID, userID int64) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM project_members
		    WHERE project_id = $1 AND user_id = $2
		 )`, projectID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check project membership: %w", err)
	}
	return exists, nil
}

// ListByProject returns all members ordered owner -> contributor ->
// viewer, then by joined_at. Joined user fields (username, display
// name, avatar) are populated so the UI doesn't need a follow-up
// roundtrip per member.
func (s *ProjectMemberStore) ListByProject(ctx context.Context, projectID int64) ([]ProjectMember, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT pm.id, pm.project_id, pm.user_id, pm.role, pm.invited_by, pm.joined_at,
		       u.username, u.display_name, u.avatar_url
		  FROM project_members pm
		  JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1
		 ORDER BY
		     CASE pm.role
		         WHEN 'owner' THEN 1
		         WHEN 'contributor' THEN 2
		         WHEN 'viewer' THEN 3
		         ELSE 4
		     END,
		     pm.joined_at`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project members: %w", err)
	}
	defer rows.Close()

	var out []ProjectMember
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.UserID, &m.Role, &m.InvitedBy, &m.JoinedAt,
			&m.Username, &m.DisplayName, &m.AvatarURL,
		); err != nil {
			return nil, fmt.Errorf("scan project member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project members: %w", err)
	}
	return out, nil
}
