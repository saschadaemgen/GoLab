-- +goose Up
-- Promote the original user (MeinPrinz, id=1) to power_level 100 so the
-- admin dashboard is reachable on the live instance. Safe against re-run:
-- if id=1 doesn't exist this is a no-op, and if id=1 is already 100 the
-- UPDATE is idempotent.
UPDATE users SET power_level = 100 WHERE id = 1;

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- Never auto-demote. A manual reviewed plan is required for any rollback.
SELECT 1;
