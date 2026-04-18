-- +goose Up
-- +goose StatementBegin

-- Sprint 15 B6: user-facing edit-post feature.
--
-- Every save of Update writes the PREVIOUS content into this table
-- before overwriting the posts row. That gives us three things for
-- free:
--
--   1. An "edited N times" badge on the post card without storing a
--      counter on the post itself.
--   2. A revert path if an admin needs to roll a bad edit back.
--   3. Audit trail for Sprint 15c admin moderation - an admin edit
--      lands here with edit_kind = 'admin' so the moderation log
--      can link back to the original content the user saw.
--
-- Only the PREVIOUS content is stored (not the new one) because the
-- new content is always the current posts row. History reads as a
-- reverse-chronological list of what the post used to be.
CREATE TABLE IF NOT EXISTS post_edit_history (
    id               BIGSERIAL   PRIMARY KEY,
    post_id          BIGINT      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    editor_id        BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    previous_content TEXT        NOT NULL,
    edit_reason      TEXT,
    -- edit_kind distinguishes user self-edit (most common) from admin
    -- moderation edits (Sprint 15c). The UI shows a different badge
    -- for each: "edited" for author, "edited by moderator" for admin.
    edit_kind        VARCHAR(16) NOT NULL DEFAULT 'author',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Feed / thread renders query "give me the latest edit time for
-- this post" to decide whether to show the badge. Indexed on
-- (post_id, created_at DESC) so the most recent entry is a single
-- row fetch.
CREATE INDEX IF NOT EXISTS idx_post_edit_history_post
    ON post_edit_history (post_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Intentionally disabled per the live-DB no-destructive-down rule.
SELECT 1;
-- +goose StatementEnd
