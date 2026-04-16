-- +goose Up
ALTER TABLE users ADD COLUMN banned BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN banned_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN banned_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- Dropping banned/banned_at/banned_reason would destroy moderation state.
-- If a rollback is truly required, do it manually with a reviewed plan.
SELECT 1;
