-- +goose Up
--
-- Sprint Y: per-applicant rating record. Admins score new
-- applications across five dimensions (one per application
-- field); the row stays attached to the user permanently and
-- can be re-edited as the user develops, even after approval.
--
-- The user never sees their own rating; this table is admin-only
-- read/write through /api/admin/users/{id}/rating.
--
-- All five rating columns are nullable: NULL means the admin has
-- not scored that dimension yet. Only the filled-in dimensions
-- count toward the average displayed in the admin panel.
--
-- ON DELETE CASCADE on user_id: if a user is hard-deleted (rare,
-- but the GDPR-erasure path), the rating goes with them. The
-- reviewer foreign key uses ON DELETE SET NULL so dropping a
-- former admin account does not cascade-wipe the moderation
-- history of every applicant they reviewed.

CREATE TABLE IF NOT EXISTS application_ratings (
    user_id                 BIGINT      PRIMARY KEY
                                        REFERENCES users(id) ON DELETE CASCADE,
    track_record            INT         CHECK (track_record IS NULL
                                            OR (track_record >= 1 AND track_record <= 10)),
    ecosystem_fit           INT         CHECK (ecosystem_fit IS NULL
                                            OR (ecosystem_fit >= 1 AND ecosystem_fit <= 10)),
    contribution_potential  INT         CHECK (contribution_potential IS NULL
                                            OR (contribution_potential >= 1 AND contribution_potential <= 10)),
    relevance               INT         CHECK (relevance IS NULL
                                            OR (relevance >= 1 AND relevance <= 10)),
    communication           INT         CHECK (communication IS NULL
                                            OR (communication >= 1 AND communication <= 10)),
    reviewer_id             BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at             TIMESTAMPTZ,
    notes                   TEXT        NOT NULL DEFAULT '',
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_application_ratings_reviewer
    ON application_ratings(reviewer_id);

-- +goose Down
-- Forward-only project policy. Restoring would not bring back
-- discarded ratings.
SELECT 1;
