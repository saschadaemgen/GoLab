-- +goose Up
CREATE TABLE IF NOT EXISTS uploads (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    filename    VARCHAR(255) NOT NULL,
    mime_type   VARCHAR(64) NOT NULL,
    size_bytes  BIGINT NOT NULL,
    url         VARCHAR(512) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_uploads_user ON uploads(user_id);

-- +goose Down
-- Intentionally disabled in production. GoLab is live.
-- Dropping uploads would orphan references from post HTML.
SELECT 1;
