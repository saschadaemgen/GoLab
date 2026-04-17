-- +goose Up
--
-- Sprint 10.5 revision: Help/Q&A and Announcements work better as
-- post types than as whole spaces (they span every topic). Restore
-- the 8 thematic Phase-1 spaces so the community is organised by
-- subject matter, and let post_type carry the "is this a question /
-- announcement" axis independently.
--
-- Migration 018 replaced the 8 thematic spaces with 5 operational
-- ones (General, Help, Showcase, Announcements, Off-Topic) and
-- remapped every post to General. This migration undoes that:
--
--   1. Detach posts + channels from their current space_id so the
--      DELETE below doesn't trip the FK.
--   2. Remove the 5 operational spaces.
--   3. Insert the 8 thematic spaces with stable slugs.
--   4. Remap every existing post to SimpleGo Ecosystem as the safe
--      default - that space exists in both old and new worlds and
--      nothing user-facing is lost. Admins can bulk-re-assign later.

UPDATE posts    SET space_id = NULL;
UPDATE channels SET space_id = NULL;

DELETE FROM spaces;

INSERT INTO spaces (name, slug, description, icon, color, sort_order) VALUES
  ('SimpleX Protocol',    'simplex',       'SMP protocol, clients, servers, relays',                                    '*', '#45BDD1', 1),
  ('Matrix / Element',    'matrix',        'Tuwunel, Element X, matrix-rust-sdk, bridges, federation',                   '#', '#0DBD8B', 2),
  ('Cybersecurity',       'cybersecurity', 'Encryption, network security, audits, CVEs, threat models',                  '!', '#E74C3C', 3),
  ('Privacy Tech',        'privacy',       'Tor, VPNs, metadata protection, PGP, Signal Protocol',                       '~', '#9B59B6', 4),
  ('Hardware Security',   'hardware',      'ESP32, HSMs, GoKey, secure elements, physical security',                     '+', '#F39C12', 5),
  ('SimpleGo Ecosystem',  'simplego',      'GoChat, GoLab, GoRelay, GoBot, GoUNITY, GoKey',                              '^', '#45BDD1', 6),
  ('Dev Tools & Code',    'devtools',      'Code reviews, libraries, frameworks, best practices',                        '>', '#2ECC71', 7),
  ('Off-Topic',           'offtopic',      'Not security related. Hobbies, fun, introductions.',                         '-', '#95A5A6', 8);

UPDATE posts
SET    space_id = (SELECT id FROM spaces WHERE slug = 'simplego')
WHERE  space_id IS NULL;

-- +goose Down
-- Intentionally disabled. Reverting would orphan posts again.
SELECT 1;
