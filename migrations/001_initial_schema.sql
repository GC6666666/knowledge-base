-- KB Knowledge Base Schema
-- Migration: 001_initial_schema.sql

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS vector;

-- Media items table
CREATE TABLE IF NOT EXISTS media_items (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_path TEXT NOT NULL,
    media_type  TEXT NOT NULL,  -- image, video, audio, text, document
    mime_type   TEXT,
    file_size   BIGINT,
    file_hash   TEXT,           -- SHA256 for deduplication
    status      TEXT NOT NULL DEFAULT 'pending',  -- pending, processing, ready, failed
    error_msg   TEXT,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Summaries table
CREATE TABLE IF NOT EXISTS summaries (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    media_id    UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    summary     TEXT,
    key_points  TEXT[],
    tags        TEXT[],
    ai_model    TEXT,
    token_count INT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(media_id)
);

-- Classifications table
CREATE TABLE IF NOT EXISTS classifications (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    media_id    UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    topic       TEXT,
    tags        TEXT[],
    confidence  FLOAT,
    ai_model    TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(media_id)
);

-- Text chunks for embedding
CREATE TABLE IF NOT EXISTS text_chunks (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    media_id    UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    chunk_index INT DEFAULT 0,
    chunk_text  TEXT NOT NULL,
    token_count INT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Embeddings table (pgvector)
CREATE TABLE IF NOT EXISTS embeddings (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chunk_id    UUID NOT NULL REFERENCES text_chunks(id) ON DELETE CASCADE,
    embedding   VECTOR(1536),  -- MiniMax embo-01 dimension
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_media_items_type ON media_items(media_type);
CREATE INDEX idx_media_items_status ON media_items(status);
CREATE INDEX idx_media_items_hash ON media_items(file_hash);
CREATE INDEX idx_media_items_created ON media_items(created_at DESC);
CREATE INDEX idx_text_chunks_media ON text_chunks(media_id);
CREATE INDEX idx_embeddings_chunk ON embeddings(chunk_id);
CREATE INDEX idx_embeddings_vector ON embeddings USING hnsw (embedding vector_cosine_ops);

-- Full-text search index
CREATE INDEX idx_text_chunks_fts ON text_chunks USING gin(to_tsvector('english', chunk_text));

-- updated_at trigger
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER media_items_updated_at
    BEFORE UPDATE ON media_items
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
