-- +goose Up
--
-- Sprint 16d: parent_project_id on projects.
--
-- Adds a self-referential nullable foreign key so any project can
-- optionally have one parent project. The application layer enforces
-- nesting depth = 1 (a sub-project cannot itself become a parent) and
-- same-space parents; the schema stays neutral so we keep options
-- open for future relaxations without another migration.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS parent_project_id BIGINT REFERENCES projects(id);

-- Partial index: only useful for "list children of project N" lookups
-- which always exclude soft-deleted rows. Matches the existing
-- soft-delete-aware pattern used by idx_projects_space_slug.
CREATE INDEX IF NOT EXISTS idx_projects_parent
    ON projects (parent_project_id)
    WHERE parent_project_id IS NOT NULL AND deleted_at IS NULL;

-- +goose Down
-- Forward-only project policy. Dropping parent_project_id would
-- orphan every sub-project link.
SELECT 1;
