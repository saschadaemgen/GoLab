-- +goose Up
--
-- Sprint 16a: Project system foundation.
--
-- A Project is a structured container for collaborative work that lives
-- inside a Space. Each Project owns its documentation (concept,
-- architecture, workflow, roadmap), runs through sequential Seasons
-- (each closed with a closing document), groups Posts under those
-- Seasons, and has Members with one of three roles.
--
-- Posts may still live directly in a Space without a Season - the new
-- posts.season_id column is NULLABLE and existing rows keep working
-- without modification.

CREATE TABLE IF NOT EXISTS projects (
    id          BIGSERIAL   PRIMARY KEY,
    space_id    BIGINT      NOT NULL REFERENCES spaces(id),
    slug        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'draft'
                            CHECK (status IN ('draft','active','archived','closed')),
    visibility  TEXT        NOT NULL DEFAULT 'public'
                            CHECK (visibility IN ('public','members_only','hidden')),
    owner_id    BIGINT      NOT NULL REFERENCES users(id),
    icon        TEXT        NOT NULL DEFAULT '',
    color       TEXT        NOT NULL DEFAULT '',
    deleted_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_space_slug
    ON projects (space_id, slug)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects (owner_id);

CREATE INDEX IF NOT EXISTS idx_projects_status
    ON projects (status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_projects_visibility
    ON projects (visibility)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS project_docs (
    id             BIGSERIAL   PRIMARY KEY,
    project_id     BIGINT      NOT NULL REFERENCES projects(id),
    doc_type       TEXT        NOT NULL
                               CHECK (doc_type IN ('concept','architecture',
                                                   'workflow','roadmap','custom')),
    title          TEXT        NOT NULL DEFAULT '',
    content_md     TEXT        NOT NULL DEFAULT '',
    content_html   TEXT        NOT NULL DEFAULT '',
    sort_order     INTEGER     NOT NULL DEFAULT 0,
    last_edited_by BIGINT      REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One concept/architecture/workflow/roadmap per project. Custom docs
-- are unlimited so they sit outside the partial unique index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_project_docs_project_type
    ON project_docs (project_id, doc_type)
    WHERE doc_type <> 'custom';

CREATE INDEX IF NOT EXISTS idx_project_docs_project ON project_docs (project_id);

CREATE TABLE IF NOT EXISTS seasons (
    id               BIGSERIAL   PRIMARY KEY,
    project_id       BIGINT      NOT NULL REFERENCES projects(id),
    season_number    INTEGER     NOT NULL,
    title            TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    status           TEXT        NOT NULL DEFAULT 'planned'
                                 CHECK (status IN ('planned','active','closed')),
    started_at       TIMESTAMPTZ,
    closed_at        TIMESTAMPTZ,
    closing_doc_md   TEXT        NOT NULL DEFAULT '',
    closing_doc_html TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_seasons_project_number
    ON seasons (project_id, season_number);

CREATE INDEX IF NOT EXISTS idx_seasons_project_status
    ON seasons (project_id, status);

CREATE TABLE IF NOT EXISTS project_members (
    id         BIGSERIAL   PRIMARY KEY,
    project_id BIGINT      NOT NULL REFERENCES projects(id),
    user_id    BIGINT      NOT NULL REFERENCES users(id),
    role       TEXT        NOT NULL DEFAULT 'viewer'
                           CHECK (role IN ('owner','contributor','viewer')),
    invited_by BIGINT      REFERENCES users(id),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_project_members_project_user
    ON project_members (project_id, user_id);

CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members (user_id);

CREATE TABLE IF NOT EXISTS project_tags (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tag_id     BIGINT NOT NULL REFERENCES tags(id)     ON DELETE CASCADE,
    PRIMARY KEY (project_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_project_tags_tag ON project_tags (tag_id);

ALTER TABLE posts ADD COLUMN IF NOT EXISTS season_id BIGINT REFERENCES seasons(id);

CREATE INDEX IF NOT EXISTS idx_posts_season
    ON posts (season_id)
    WHERE season_id IS NOT NULL;

-- +goose Down
-- Forward-only project policy. Dropping these tables would orphan
-- project_members, seasons, docs, and the season_id column on posts.
SELECT 1;
