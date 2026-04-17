-- +goose Up
CREATE TABLE IF NOT EXISTS spaces (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(64) NOT NULL,
    slug        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    icon        VARCHAR(8) NOT NULL DEFAULT '',
    color       VARCHAR(7) NOT NULL DEFAULT '#45BDD1',
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_spaces_sort ON spaces(sort_order);

-- Seed the eight default spaces. ON CONFLICT keeps the seed idempotent
-- in case the migration is replayed (e.g. DB restore drill).
INSERT INTO spaces (name, slug, description, icon, color, sort_order) VALUES
('SimpleX Protocol',   'simplex',       'SMP protocol, client development, server setup, relay architecture',  '*', '#45BDD1', 1),
('Matrix / Element',   'matrix',        'Tuwunel, Element X, matrix-rust-sdk, bridges, federation',           '#', '#0DBD8B', 2),
('Cybersecurity',      'cybersecurity', 'Encryption, network security, audits, CVEs, threat models',          '!', '#E74C3C', 3),
('Privacy Tech',       'privacy',       'Tor, VPNs, metadata protection, PGP, Signal Protocol',               '~', '#9B59B6', 4),
('Hardware Security',  'hardware',      'ESP32, HSMs, GoKey, secure elements, physical security',             '+', '#F39C12', 5),
('SimpleGo Ecosystem', 'simplego',      'GoChat, GoLab, GoRelay, GoBot, GoUNITY, GoKey',                      '^', '#45BDD1', 6),
('Dev Tools & Code',   'devtools',      'Code reviews, libraries, frameworks, best practices',                '>', '#2ECC71', 7),
('Off-Topic / Meta',   'meta',          'Community, feedback, introductions, GoLab feature requests',         '-', '#95A5A6', 8)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
-- Intentionally disabled in production. GoLab is live.
-- Dropping spaces would orphan posts and channels via space_id FK.
-- Manual reviewed rollback only.
SELECT 1;
