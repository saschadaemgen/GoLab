package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplicationRating is the moderation-only score record attached
// to a user. Sprint Y: admins fill it in while reviewing the
// application form during approval, then optionally update later
// as the user develops. None of these fields are exposed through
// the public profile API or the user-facing notification feed.
//
// Each dimension is *int because NULL has a real meaning in the
// schema: "the admin has not rated this dimension yet". A NULL
// dimension is excluded from Average.
type ApplicationRating struct {
	UserID                int64      `json:"user_id"`
	TrackRecord           *int       `json:"track_record,omitempty"`
	EcosystemFit          *int       `json:"ecosystem_fit,omitempty"`
	ContributionPotential *int       `json:"contribution_potential,omitempty"`
	Relevance             *int       `json:"relevance,omitempty"`
	Communication         *int       `json:"communication,omitempty"`
	ReviewerID            *int64     `json:"reviewer_id,omitempty"`
	ReviewedAt            *time.Time `json:"reviewed_at,omitempty"`
	Notes                 string     `json:"notes"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// dimensions returns every rating pointer in a stable order so
// Average and RatedCount iterate the same set without
// duplicating the field list. New dimensions added to the schema
// must also be added here.
func (r *ApplicationRating) dimensions() []*int {
	return []*int{
		r.TrackRecord,
		r.EcosystemFit,
		r.ContributionPotential,
		r.Relevance,
		r.Communication,
	}
}

// Average returns the mean of the non-nil dimensions, or 0 when
// nothing is rated yet. Zero is a safe sentinel because a real
// rating cannot be 0 (CHECK constraint enforces 1-10) - any
// non-zero average means at least one dimension has been scored.
func (r *ApplicationRating) Average() float64 {
	if r == nil {
		return 0
	}
	var sum, count int
	for _, v := range r.dimensions() {
		if v != nil {
			sum += *v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

// RatedCount reports how many of the five dimensions have a value.
// Used by the admin UI to show "3/5 dimensions rated" alongside
// the average.
func (r *ApplicationRating) RatedCount() int {
	if r == nil {
		return 0
	}
	var n int
	for _, v := range r.dimensions() {
		if v != nil {
			n++
		}
	}
	return n
}

// AllowedRatingDimensions is the validation allow-list shared by
// the admin handler (rejects unknown column names) and the store
// (interpolates the column name into UPDATE SQL). Keeping the set
// here means a new dimension only needs to be added to the
// migration, the struct, the dimensions() slice and this map -
// no SQL injection concern from a stale list elsewhere.
var AllowedRatingDimensions = map[string]bool{
	"track_record":           true,
	"ecosystem_fit":          true,
	"contribution_potential": true,
	"relevance":              true,
	"communication":          true,
}

// ApplicationRatingStore persists rating rows.
type ApplicationRatingStore struct {
	DB *pgxpool.Pool
}

const ratingColumns = `
    user_id, track_record, ecosystem_fit, contribution_potential,
    relevance, communication, reviewer_id, reviewed_at, notes, updated_at`

// scanRating fills r from a pgx Row in the order ratingColumns
// declares. Used by Get and (in future) listing variants.
func scanRating(row pgx.Row, r *ApplicationRating) error {
	return row.Scan(
		&r.UserID,
		&r.TrackRecord, &r.EcosystemFit, &r.ContributionPotential,
		&r.Relevance, &r.Communication,
		&r.ReviewerID, &r.ReviewedAt,
		&r.Notes, &r.UpdatedAt,
	)
}

// Get returns the rating for userID. When no row exists yet a
// zero-value ApplicationRating with that user id is returned
// (never nil), so callers can render "0/5 dimensions rated" in
// the admin UI without a separate "not started" branch.
func (s *ApplicationRatingStore) Get(ctx context.Context, userID int64) (*ApplicationRating, error) {
	r := &ApplicationRating{UserID: userID}
	err := scanRating(s.DB.QueryRow(ctx,
		`SELECT `+ratingColumns+` FROM application_ratings WHERE user_id = $1`,
		userID,
	), r)
	if err == pgx.ErrNoRows {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rating: %w", err)
	}
	return r, nil
}

// SetDimension upserts the row for userID and assigns `value`
// (1-10 or nil to clear) to the named dimension. The dimension
// string MUST be in AllowedRatingDimensions; the caller is
// expected to validate before calling, but the store enforces
// the same rule defensively to make SQL injection impossible
// even if a future caller forgets the check.
//
// reviewer + reviewed_at are stamped on every update so the
// admin panel can show "rated by maria, 3 hours ago" later.
func (s *ApplicationRatingStore) SetDimension(ctx context.Context, userID int64, dimension string, value *int, reviewerID int64) error {
	if !AllowedRatingDimensions[dimension] {
		return fmt.Errorf("invalid dimension: %s", dimension)
	}
	if value != nil && (*value < 1 || *value > 10) {
		return fmt.Errorf("rating value out of range: %d", *value)
	}
	// We use an INSERT ... ON CONFLICT upsert so the first time a
	// dimension is set the row is created; subsequent edits hit the
	// UPDATE branch. Concatenating `dimension` into the SQL is safe
	// because the allow-list above blocked anything not in the
	// fixed five-name set.
	query := `
		INSERT INTO application_ratings
		    (user_id, ` + dimension + `, reviewer_id, reviewed_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
		    ` + dimension + ` = EXCLUDED.` + dimension + `,
		    reviewer_id = EXCLUDED.reviewer_id,
		    reviewed_at = EXCLUDED.reviewed_at,
		    updated_at = NOW()`
	_, err := s.DB.Exec(ctx, query, userID, value, reviewerID)
	if err != nil {
		return fmt.Errorf("set rating dimension: %w", err)
	}
	return nil
}

// SetNotes stores the admin's free-form notes blob. Capped at
// 2000 chars at the handler layer; the column is TEXT so the DB
// has no opinion. Stamps reviewer + reviewed_at like SetDimension.
func (s *ApplicationRatingStore) SetNotes(ctx context.Context, userID int64, notes string, reviewerID int64) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO application_ratings
		    (user_id, notes, reviewer_id, reviewed_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 ON CONFLICT (user_id) DO UPDATE SET
		    notes = EXCLUDED.notes,
		    reviewer_id = EXCLUDED.reviewer_id,
		    reviewed_at = EXCLUDED.reviewed_at,
		    updated_at = NOW()`,
		userID, notes, reviewerID,
	)
	if err != nil {
		return fmt.Errorf("set rating notes: %w", err)
	}
	return nil
}

// AttachTo loads ratings in bulk for a slice of users so the
// admin pending-users view does not N+1 the DB. Returns a map
// keyed by user id; users with no rating row get a zero-value
// rating in the map.
func (s *ApplicationRatingStore) AttachTo(ctx context.Context, userIDs []int64) (map[int64]*ApplicationRating, error) {
	out := make(map[int64]*ApplicationRating, len(userIDs))
	for _, id := range userIDs {
		out[id] = &ApplicationRating{UserID: id}
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.DB.Query(ctx,
		`SELECT `+ratingColumns+` FROM application_ratings WHERE user_id = ANY($1)`,
		userIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("attach ratings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r ApplicationRating
		if err := scanRating(rows, &r); err != nil {
			return nil, fmt.Errorf("scan rating: %w", err)
		}
		out[r.UserID] = &r
	}
	return out, nil
}
