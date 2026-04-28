-- +goose Up
--
-- Sprint 17: Reading activity tracking.
--
-- Discourse-style metrics: cumulative active reading time, distinct
-- posts read, distinct days visited, distinct topics entered. Server
-- is the truth: the client emits short heartbeats describing what is
-- visible and whether the user is active, the server validates,
-- caps, and writes to four tables.
--
-- reading_events       - raw observations, append-only, partitioned
--                        by month so old data can be archived /
--                        dropped one partition at a time.
-- user_reading_stats   - aggregated per-user counters, the
--                        projection profile pages read from.
-- user_visits          - one row per (user, day) so distinct-days
--                        is a SELECT COUNT(*).
-- topic_views          - one row per (user, topic) so topics-entered
--                        is a SELECT COUNT(*). The "topic" is just a
--                        post id (root-of-thread by convention).
--
-- Five settings rows let the Prinz tune tracker behaviour at runtime
-- without redeploying. The keys all share the reading_ prefix so a
-- future Site Settings UI can group them.

CREATE TABLE IF NOT EXISTS reading_events (
    id              BIGSERIAL,
    user_id         BIGINT      NOT NULL REFERENCES users(id),
    post_id         BIGINT      NOT NULL REFERENCES posts(id),
    seconds_visible REAL        NOT NULL DEFAULT 0,
    counted_as_read BOOLEAN     NOT NULL DEFAULT FALSE,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (last_seen_at, id)
) PARTITION BY RANGE (last_seen_at);

-- Explicit partitions for the next few months. A maintenance job
-- (out of scope for Sprint 17) will pre-create future partitions.
-- Until then the default partition catches anything outside the
-- explicit ranges so writes never fail.
CREATE TABLE IF NOT EXISTS reading_events_2026_04 PARTITION OF reading_events
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE IF NOT EXISTS reading_events_2026_05 PARTITION OF reading_events
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE IF NOT EXISTS reading_events_2026_06 PARTITION OF reading_events
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE IF NOT EXISTS reading_events_2026_07 PARTITION OF reading_events
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE IF NOT EXISTS reading_events_2026_08 PARTITION OF reading_events
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE IF NOT EXISTS reading_events_2026_09 PARTITION OF reading_events
    FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE IF NOT EXISTS reading_events_default PARTITION OF reading_events DEFAULT;

CREATE INDEX IF NOT EXISTS idx_reading_events_user_time
    ON reading_events (user_id, last_seen_at DESC);

-- Hot-path lookup: "did this user already see this post recently?"
CREATE INDEX IF NOT EXISTS idx_reading_events_user_post_time
    ON reading_events (user_id, post_id, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS user_reading_stats (
    user_id               BIGINT      PRIMARY KEY REFERENCES users(id),
    total_seconds_active  BIGINT      NOT NULL DEFAULT 0,
    posts_read_count      INTEGER     NOT NULL DEFAULT 0,
    topics_entered_count  INTEGER     NOT NULL DEFAULT 0,
    distinct_days_visited INTEGER     NOT NULL DEFAULT 0,
    last_active_at        TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_visits (
    user_id    BIGINT NOT NULL REFERENCES users(id),
    visit_date DATE   NOT NULL,
    PRIMARY KEY (user_id, visit_date)
);

CREATE INDEX IF NOT EXISTS idx_user_visits_date ON user_visits (visit_date);

CREATE TABLE IF NOT EXISTS topic_views (
    user_id         BIGINT      NOT NULL REFERENCES users(id),
    topic_id        BIGINT      NOT NULL REFERENCES posts(id),
    first_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, topic_id)
);

-- Tracker tunables. The hot path reads these on every heartbeat so
-- they should stay in a small enough cache; ReadingStore.LoadConfig
-- pulls them in one query and parses the strings into the typed
-- ReadingConfig struct.
INSERT INTO settings (key, value) VALUES
    ('reading_min_seconds_per_post',           '4'),
    ('reading_heartbeat_interval_seconds',    '15'),
    ('reading_idle_timeout_seconds',          '30'),
    ('reading_max_active_seconds_per_minute', '60'),
    ('reading_max_posts_per_heartbeat',       '20')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- Forward-only project policy. Dropping these tables would discard
-- accumulated reading-time history that drives the Trust Level
-- system in Sprint 19.
SELECT 1;
