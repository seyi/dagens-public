-- 002_create_artifacts.sql
-- Creates artifacts table for persistent artifact storage

CREATE TABLE IF NOT EXISTS artifacts (
    session_id   VARCHAR(255) NOT NULL,
    name         VARCHAR(255) NOT NULL,
    data         BYTEA NOT NULL,
    size         BIGINT NOT NULL,
    created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, name)
);

-- Index for session-based queries
CREATE INDEX IF NOT EXISTS idx_artifacts_session_id ON artifacts(session_id);

-- Index for size-based queries (for analytics)
CREATE INDEX IF NOT EXISTS idx_artifacts_size ON artifacts(size);

-- Index for time-based queries
CREATE INDEX IF NOT EXISTS idx_artifacts_created_at ON artifacts(created_at);

COMMENT ON TABLE artifacts IS 'Stores binary artifacts (files, images, etc.)';
COMMENT ON COLUMN artifacts.session_id IS 'Session that owns this artifact';
COMMENT ON COLUMN artifacts.name IS 'Artifact name/path';
COMMENT ON COLUMN artifacts.data IS 'Binary data stored as BYTEA';
COMMENT ON COLUMN artifacts.size IS 'Size in bytes (for monitoring)';
