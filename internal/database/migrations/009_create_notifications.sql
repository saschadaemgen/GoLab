-- +goose Up
CREATE TABLE notifications (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notif_type      VARCHAR(16) NOT NULL,
    -- reaction, reply, follow, mention
    post_id         BIGINT REFERENCES posts(id) ON DELETE CASCADE,
    is_read         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_user ON notifications(user_id, is_read, created_at DESC);
CREATE INDEX idx_notifications_actor ON notifications(actor_id);

-- +goose Down
-- Intentionally disabled in production. GoLab is live.
-- Dropping notifications would destroy user notification history.
-- If a rollback is truly required, do it manually with a reviewed plan.
SELECT 1;
