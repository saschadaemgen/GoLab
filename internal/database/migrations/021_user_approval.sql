-- +goose Up
--
-- Sprint 12 moderation: user approval workflow.
--
-- Adds status/reviewed fields to users and a simple key/value
-- settings table so admins can toggle require_approval at runtime.
--
-- IMPORTANT: existing users default to status = 'active' so nobody
-- gets locked out by this migration. Only NEW registrations under
-- require_approval will land as 'pending'.

ALTER TABLE users ADD COLUMN IF NOT EXISTS status      VARCHAR(16)  NOT NULL DEFAULT 'active';
ALTER TABLE users ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS reviewed_by BIGINT REFERENCES users(id);

CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

CREATE TABLE IF NOT EXISTS settings (
    key        VARCHAR(64) PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO settings (key, value) VALUES
    ('require_approval', 'true')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- Intentionally disabled. Removing status would flip every user back
-- to an unreviewed state and wipe moderation history.
SELECT 1;
