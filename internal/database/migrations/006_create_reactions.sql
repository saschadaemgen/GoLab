-- +goose Up
CREATE TABLE reactions (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id         BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    reaction_type   VARCHAR(16) NOT NULL DEFAULT 'like',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

CREATE INDEX idx_reactions_post ON reactions(post_id);

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- If a rollback is truly required, do it manually with a reviewed plan.
-- Never auto-drop in live systems.
SELECT 1;
