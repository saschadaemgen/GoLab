-- +goose Up
--
-- Sprint Y.1: knowledge-question fields. Added to the user row so
-- the registration form can capture them once and the admin panel
-- can read them during review without a join. They live with the
-- other application fields (ecosystem_connection, etc) added in
-- Sprint X.
--
-- The four new columns:
--
--   technical_depth_choice   one of '', 'a', 'b', 'c'. Empty means
--                            the user has not picked a sub-question
--                            (only valid for legacy rows from before
--                            this sprint; new applications must
--                            pick one).
--   technical_depth_answer   100-500 chars per the handler-layer
--                            validator. The TEXT column has no
--                            length cap so the constraint lives in
--                            Go; this matches how every other
--                            application field is bounded.
--   practical_experience     optional, 0-400 chars at the handler
--                            layer.
--   critical_thinking        optional, 0-400 chars at the handler
--                            layer.
--
-- Existing rows default to empty strings on every new column, so
-- nobody is forced to retro-fill knowledge questions just because
-- the migration ran. The CHECK constraint allows the empty string
-- specifically so those legacy rows do not break on the next read.

ALTER TABLE users ADD COLUMN IF NOT EXISTS technical_depth_choice TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS technical_depth_answer TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS practical_experience   TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS critical_thinking      TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'technical_depth_choice_valid'
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT technical_depth_choice_valid
            CHECK (technical_depth_choice IN ('', 'a', 'b', 'c'));
    END IF;
END$$;

-- +goose Down
-- Forward-only project policy.
SELECT 1;
