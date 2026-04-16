-- +goose Up
CREATE TABLE channel_members (
    channel_id      BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    power_level     INTEGER NOT NULL DEFAULT 10,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX idx_channel_members_user ON channel_members(user_id);

-- +goose Down
DROP TABLE IF EXISTS channel_members;
