-- +goose Up
--
-- Sprint 10.5: ensure one reaction per (user, post, reaction_type).
-- The `reactions` table already has PRIMARY KEY (user_id, post_id) from
-- migration 006, which only allowed ONE reaction per user per post
-- regardless of type. Sprint 10.5 lets users react with multiple
-- different emojis (heart + fire + thumbsup all at once), so we need
-- a wider uniqueness on the triple instead of the pair.
--
-- Strategy:
--   1. Add the new triple-uniqueness constraint via a unique INDEX
--      (indices can be added even while the old PK exists).
--   2. Leave the old PK in place until we can drop it; we'd have to
--      drop the PK to let users have multiple rows per post, but the
--      "never drop" rule forbids that. So instead:
--   3. The backend toggles rows by (user_id, post_id, reaction_type).
--      When a user reacts with a NEW type, the old PK blocks the
--      insert. We have to work around this in application code: on
--      react, first check if a row exists for this user+post at all -
--      if yes with a different type, UPDATE the row's reaction_type
--      instead of INSERTing a new one. Until the PK can be widened.

-- Idempotent unique index across the full triple.
CREATE UNIQUE INDEX IF NOT EXISTS idx_reactions_unique_triple
  ON reactions(user_id, post_id, reaction_type);

-- +goose Down
-- Intentionally disabled. Dropping the index would let duplicate
-- reaction rows reappear which the backend toggle logic treats as a
-- unique key.
SELECT 1;
