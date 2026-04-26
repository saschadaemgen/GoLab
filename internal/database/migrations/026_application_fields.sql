-- +goose Up
--
-- Sprint X: application-based registration with curated write access.
--
-- GoLab moves from open email-based registration to a curated
-- application form. The five application fields below are filled in
-- on /register, reviewed by admins on /admin, and stay on the user
-- row for moderation history. The existing pending/active/rejected
-- workflow from migration 021 is reused as-is.
--
-- The email column is dropped permanently. GoLab never sent any
-- email (no confirmation, no notification, no recovery), so the
-- column was unused infrastructure that also conflicted with the
-- privacy goals of the SimpleGo ecosystem. Existing emails are
-- discarded by this migration. Forward-only project policy.

DROP INDEX IF EXISTS idx_users_email;
ALTER TABLE users DROP COLUMN IF EXISTS email;

ALTER TABLE users ADD COLUMN IF NOT EXISTS external_links         TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS ecosystem_connection   TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS community_contribution TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS current_focus          TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS application_notes      TEXT NOT NULL DEFAULT '';

-- Make sure the require_approval gate is on. Default is already 'true'
-- per migration 021 but we re-assert here so a sysadmin who turned it
-- off in production gets it back on after this sprint deploys: write
-- access is application-only from now on, regardless of the toggle.
INSERT INTO settings (key, value) VALUES
    ('require_approval', 'true')
ON CONFLICT (key) DO UPDATE SET value = 'true', updated_at = NOW();

-- +goose Down
-- Intentionally disabled. Forward-only project policy. Restoring
-- the email column would not bring back any of the data we just
-- discarded.
SELECT 1;
