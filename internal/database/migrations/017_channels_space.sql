-- +goose Up
-- Channels are no longer the top-level organization, Spaces are. Each
-- channel belongs to a Space. The column is NULL-able so channels from
-- before this migration still work even if the backfill below somehow
-- fails.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS space_id BIGINT REFERENCES spaces(id);

-- Map every existing channel to the 'meta' space as a safe default.
-- Admins can re-assign individual channels via the admin panel later.
UPDATE channels
SET    space_id = (SELECT id FROM spaces WHERE slug = 'meta')
WHERE  space_id IS NULL
  AND  EXISTS (SELECT 1 FROM spaces WHERE slug = 'meta');

CREATE INDEX IF NOT EXISTS idx_channels_space ON channels(space_id);

-- +goose Down
-- Intentionally disabled. Dropping the column would lose every channel's
-- space assignment.
SELECT 1;
