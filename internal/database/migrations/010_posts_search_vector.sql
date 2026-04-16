-- +goose Up
ALTER TABLE posts
  ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('english', coalesce(content, ''))) STORED;

CREATE INDEX idx_posts_search ON posts USING GIN(search_vector);

-- +goose Down
-- Intentionally disabled in production. GoLab is live.
-- Dropping search_vector/index would break search; trivially rebuildable
-- but still requires a reviewed plan.
SELECT 1;
