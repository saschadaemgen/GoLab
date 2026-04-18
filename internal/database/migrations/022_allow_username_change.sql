-- +goose Up
-- Sprint 13: admin-controlled toggle for whether users can change
-- their own username. Admins can ALWAYS change usernames regardless
-- of this setting; it only gates the user-facing settings form.
INSERT INTO settings (key, value) VALUES
    ('allow_username_change', 'true')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- Intentionally disabled. Deleting the setting would flip the
-- runtime behaviour silently.
SELECT 1;
