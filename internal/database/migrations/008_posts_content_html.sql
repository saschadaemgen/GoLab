-- +goose Up
ALTER TABLE posts ADD COLUMN content_html TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real post data.
-- Dropping content_html would destroy rendered HTML for all existing posts.
-- If a rollback is truly required, do it manually with a reviewed plan.
SELECT 1;
