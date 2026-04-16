-- +goose Up
CREATE TABLE channels (
    id              BIGSERIAL PRIMARY KEY,
    slug            VARCHAR(64) UNIQUE NOT NULL,
    name            VARCHAR(128) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    channel_type    VARCHAR(16) NOT NULL DEFAULT 'public',
    creator_id      BIGINT NOT NULL REFERENCES users(id),
    power_required  INTEGER NOT NULL DEFAULT 10,
    member_count    INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_channels_slug ON channels(slug);
CREATE INDEX idx_channels_type ON channels(channel_type);

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- If a rollback is truly required, do it manually with a reviewed plan.
-- Never auto-drop in live systems.
SELECT 1;
