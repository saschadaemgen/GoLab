-- +goose Up
CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    username        VARCHAR(32) UNIQUE NOT NULL,
    email           VARCHAR(255) UNIQUE NOT NULL,
    password_hash   VARCHAR(255) NOT NULL,
    display_name    VARCHAR(64) NOT NULL DEFAULT '',
    bio             TEXT NOT NULL DEFAULT '',
    avatar_url      VARCHAR(512) NOT NULL DEFAULT '',
    power_level     INTEGER NOT NULL DEFAULT 10,
    did_key         VARCHAR(512) DEFAULT NULL,
    cert_fingerprint VARCHAR(128) DEFAULT NULL,
    hardware_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_did_key ON users(did_key) WHERE did_key IS NOT NULL;

-- +goose Down
-- Intentionally disabled in production. GoLab is live with real user data.
-- If a rollback is truly required, do it manually with a reviewed plan.
-- Never auto-drop in live systems.
SELECT 1;
