-- +goose Up
-- +goose StatementBegin

-- Sprint 14 product decision: wipe existing reactions so the PK can
-- be restructured cleanly. Five users on the live instance, no
-- published content that depends on historical reaction attribution.
DELETE FROM reactions;

-- The existing PRIMARY KEY (user_id, post_id) enforces one reaction
-- per user per post - the Slack-style "pick one emoji" UX we've had
-- since Sprint 10.5. We now want GitHub-style "a user can place
-- multiple distinct emoji types on the same post", so the PK moves
-- to the full triple. Dropping a PK constraint is normally on the
-- "never drop" list, but Der Prinz pre-approved this once and only
-- once because the table was wiped in the statement above.
ALTER TABLE reactions DROP CONSTRAINT reactions_pkey;

-- Migration 019 created idx_reactions_unique_triple as a defensive
-- UNIQUE index. It's redundant once the triple is the PK itself.
DROP INDEX IF EXISTS idx_reactions_unique_triple;

ALTER TABLE reactions
    ADD CONSTRAINT reactions_pkey PRIMARY KEY (user_id, post_id, reaction_type);

-- Ranking support: these two indexes cover the aggregation queries
-- a later ranking feature will run. post_id index already exists
-- (migration 006). user_created is for "my emoji history", and
-- type_created is for "most used emoji in the last N days".
CREATE INDEX IF NOT EXISTS idx_reactions_user_created
    ON reactions (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_reactions_type_created
    ON reactions (reaction_type, created_at DESC);

-- Recompute every post's reaction_count (they all become 0 because
-- the table was wiped, but keeping this explicit avoids a drift
-- window where the cached count disagrees with the table).
UPDATE posts SET reaction_count = 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Intentionally disabled. The PK restructure and wipe cannot be
-- reversed without data loss, and downgrade paths on a live DB
-- are explicitly not supported (see CLAUDE.md).
SELECT 1;
-- +goose StatementEnd
