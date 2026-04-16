-- +goose Up
CREATE TABLE posts (
    id              BIGSERIAL PRIMARY KEY,
    as_type         VARCHAR(32) NOT NULL DEFAULT 'Note',
    author_id       BIGINT NOT NULL REFERENCES users(id),
    channel_id      BIGINT REFERENCES channels(id),
    parent_id       BIGINT REFERENCES posts(id),
    repost_of_id    BIGINT REFERENCES posts(id),
    content         TEXT NOT NULL DEFAULT '',
    as_payload      JSONB DEFAULT NULL,
    as_signature    TEXT DEFAULT NULL,
    reaction_count  INTEGER NOT NULL DEFAULT 0,
    reply_count     INTEGER NOT NULL DEFAULT 0,
    repost_count    INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_posts_author ON posts(author_id);
CREATE INDEX idx_posts_channel ON posts(channel_id);
CREATE INDEX idx_posts_parent ON posts(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX idx_posts_created ON posts(created_at DESC);
CREATE INDEX idx_posts_channel_created ON posts(channel_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS posts;
