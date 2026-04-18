-- +goose Up
-- +goose StatementBegin

-- Sprint 14 @mentions. Stored denormalised in a join table so:
--   1. Render path is fast - no regex at read time.
--   2. "Which posts mention me?" is a single indexed lookup.
--   3. User / post deletions cascade cleanly (FK ON DELETE CASCADE).
--
-- The table is append-only on post create; post edits call
-- RecordMentions + a pre-delete sync to keep the set current. The
-- composite PK doubles as the dedupe constraint: a post can mention
-- the same user only once.
CREATE TABLE IF NOT EXISTS mentions (
    post_id        BIGINT      NOT NULL REFERENCES posts(id)  ON DELETE CASCADE,
    mentioned_user BIGINT      NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, mentioned_user)
);

-- The PK already indexes (post_id, mentioned_user). Add the reverse
-- direction so "show me every post that mentions user X, newest
-- first" is a single index scan.
CREATE INDEX IF NOT EXISTS idx_mentions_user_created
    ON mentions (mentioned_user, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Intentionally disabled per the live-DB no-destructive-down rule.
SELECT 1;
-- +goose StatementEnd
