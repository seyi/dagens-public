-- 001_create_sessions.sql
-- Creates sessions table for persistent session storage

CREATE TABLE IF NOT EXISTS sessions (
    id           VARCHAR(255) PRIMARY KEY,
    user_id      VARCHAR(255) NOT NULL,
    state        JSONB,
    event_history JSONB,
    created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Index for user-based queries
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);

-- Index for time-based queries
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at);

-- Index for composite queries
CREATE INDEX IF NOT EXISTS idx_sessions_user_updated ON sessions(user_id, updated_at DESC);

COMMENT ON TABLE sessions IS 'Stores agent session state and event history';
COMMENT ON COLUMN sessions.id IS 'Unique session identifier';
COMMENT ON COLUMN sessions.user_id IS 'User who owns this session';
COMMENT ON COLUMN sessions.state IS 'Session state as JSON';
COMMENT ON COLUMN sessions.event_history IS 'Array of events as JSON';
