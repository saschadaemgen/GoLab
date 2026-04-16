-- +goose Up
CREATE TABLE follows (
    follower_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    following_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, following_id)
);

CREATE INDEX idx_follows_following ON follows(following_id);

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- If a rollback is truly required, do it manually with a reviewed plan.
-- Never auto-drop in live systems.
SELECT 1;
