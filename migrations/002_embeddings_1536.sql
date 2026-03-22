-- KB Knowledge Base Schema
-- Migration: 002_embeddings_1536.sql

DROP INDEX IF EXISTS idx_embeddings_vector;

ALTER TABLE embeddings
    ALTER COLUMN embedding TYPE VECTOR(1536);

CREATE INDEX idx_embeddings_vector ON embeddings USING hnsw (embedding vector_cosine_ops);
