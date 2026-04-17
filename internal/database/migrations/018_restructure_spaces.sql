-- +goose Up
--
-- Sprint 10.5 community restructure: collapse the 8 Phase-1 spaces
-- down to 5 admin-fixed areas (General, Help, Showcase, Announcements,
-- Off-Topic). Channels stay in the database but are hidden from the UI.
--
-- Strategy:
--   1. Detach every post and channel from its current space_id so the
--      FK doesn't block the DELETE below.
--   2. Clear the spaces table.
--   3. Insert the five new spaces with stable slugs.
--   4. Remap every existing post to "general" so nothing disappears
--      from the live feed.
--
-- Channels intentionally keep space_id = NULL after this migration.
-- The UI no longer exposes them so the unassigned state is fine; if
-- Phase 2 ever brings channels back we can remap them deliberately.

UPDATE posts    SET space_id = NULL;
UPDATE channels SET space_id = NULL;

DELETE FROM spaces;

INSERT INTO spaces (name, slug, description, icon, color, sort_order) VALUES
  ('General',       'general',       'Main feed. Discussions, news, ideas - everything starts here.',                '*', '#45BDD1', 1),
  ('Help / Q&A',    'help',          'Got a question? Ask here. Posts can be marked as solved.',                     '?', '#9B59B6', 2),
  ('Showcase',      'showcase',      'Show what you built. Projects, tools, writeups, demos.',                       '+', '#2ECC71', 3),
  ('Announcements', 'announcements', 'Platform news and releases. Admin-only posting.',                              '!', '#E74C3C', 4),
  ('Off-Topic',     'offtopic',      'Not security related. Hobbies, fun, introductions.',                           '~', '#95A5A6', 5);

UPDATE posts
SET    space_id = (SELECT id FROM spaces WHERE slug = 'general')
WHERE  space_id IS NULL;

-- +goose Down
-- Intentionally disabled in production. Re-creating the old 8 spaces
-- requires a reviewed restore plan.
SELECT 1;
