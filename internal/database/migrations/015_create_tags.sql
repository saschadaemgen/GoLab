-- +goose Up
CREATE TABLE IF NOT EXISTS tags (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(32) NOT NULL,
    slug        VARCHAR(32) NOT NULL UNIQUE,
    use_count   BIGINT NOT NULL DEFAULT 0,
    created_by  BIGINT REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS post_tags (
    post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id  BIGINT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_post_tags_tag ON post_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_tags_slug ON tags(slug);
CREATE INDEX IF NOT EXISTS idx_tags_use_count ON tags(use_count DESC);

-- Seed common tags used by the default spaces. Idempotent via ON CONFLICT.
INSERT INTO tags (name, slug) VALUES
('smp-protocol',        'smp-protocol'),
('e2e-encryption',      'e2e-encryption'),
('websocket',           'websocket'),
('docker',              'docker'),
('nginx',               'nginx'),
('post-quantum',        'post-quantum'),
('rust',                'rust'),
('golang',              'golang'),
('esp32',               'esp32'),
('key-management',      'key-management'),
('self-hosting',        'self-hosting'),
('penetration-testing', 'penetration-testing'),
('code-review',         'code-review'),
('tutorial',            'tutorial'),
('question',            'question'),
('showcase',            'showcase'),
('bug-report',          'bug-report'),
('matrix-sdk',          'matrix-sdk'),
('simplex-chat',        'simplex-chat'),
('linux',               'linux')
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
-- Intentionally disabled in production. Dropping tags would destroy
-- the post_tags join rows and any user-created tags.
SELECT 1;
