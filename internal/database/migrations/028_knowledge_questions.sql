-- +goose Up
--
-- Sprint Y.1: knowledge-question fields...
-- (alles bis zur ALTER TABLE Sektion bleibt gleich)
--

ALTER TABLE users ADD COLUMN IF NOT EXISTS technical_depth_choice TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS technical_depth_answer TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS practical_experience   TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS critical_thinking      TEXT NOT NULL DEFAULT '';

-- Sprint Y.1.1: DO block with internal semicolons needs goose
-- statement markers so the whole block ships as one statement.

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose Down
-- Forward-only project policy.
SELECT 1;