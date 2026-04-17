-- +goose Up
-- Add space_id and post_type to posts. Both have sane defaults so all
-- existing rows stay valid without further rewriting: space_id is NULL-
-- able (legacy posts may not belong to a space yet), post_type defaults
-- to 'discussion'.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS space_id  BIGINT REFERENCES spaces(id);
ALTER TABLE posts ADD COLUMN IF NOT EXISTS post_type VARCHAR(16) NOT NULL DEFAULT 'discussion';

-- Fill in space_id for every existing row so the live feed keeps showing
-- them. We use the 'meta' space as a catch-all. If the 'meta' space
-- somehow doesn't exist, rows stay NULL (query joins use LEFT JOIN).
UPDATE posts
SET    space_id = (SELECT id FROM spaces WHERE slug = 'meta')
WHERE  space_id IS NULL
  AND  EXISTS (SELECT 1 FROM spaces WHERE slug = 'meta');

CREATE INDEX IF NOT EXISTS idx_posts_space ON posts(space_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_type  ON posts(post_type);

-- +goose Down
-- Intentionally disabled in production. Dropping space_id would orphan
-- the category of every existing post.
SELECT 1;
