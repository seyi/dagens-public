-- 003_create_memories.sql
-- Creates memories table with pgvector extension for semantic search

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS memories (
    id          VARCHAR(255) PRIMARY KEY,
    user_id     VARCHAR(255) NOT NULL,
    content     TEXT NOT NULL,
    embedding   vector(1536),  -- OpenAI ada-002 dimension
    metadata    JSONB,
    created_at  TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Index for user-based queries
CREATE INDEX IF NOT EXISTS idx_memories_user_id ON memories(user_id);

-- Index for time-based queries
CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at);

-- IVFFlat index for fast approximate nearest neighbor search
-- Lists = sqrt(total_rows) is a good heuristic, starting with 100
CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);

-- Note: For HNSW index (PostgreSQL 16+ with pgvector 0.5.0+), use:
-- CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories
-- USING hnsw (embedding vector_cosine_ops)
-- WITH (m = 16, ef_construction = 64);

COMMENT ON TABLE memories IS 'Stores long-term memories with vector embeddings';
COMMENT ON COLUMN memories.id IS 'Unique memory identifier';
COMMENT ON COLUMN memories.user_id IS 'User who owns this memory';
COMMENT ON COLUMN memories.content IS 'Memory content (text)';
COMMENT ON COLUMN memories.embedding IS 'Vector embedding for semantic search';
COMMENT ON COLUMN memories.metadata IS 'Additional metadata as JSON';
