-- KB Knowledge Base Schema
-- Migration: 003_ai_provider_metadata.sql

ALTER TABLE summaries
    ADD COLUMN IF NOT EXISTS ai_provider TEXT;

ALTER TABLE classifications
    ADD COLUMN IF NOT EXISTS ai_provider TEXT;

UPDATE summaries
SET ai_provider = COALESCE(ai_provider, 'unknown')
WHERE ai_provider IS NULL;

UPDATE classifications
SET ai_provider = COALESCE(ai_provider, 'unknown')
WHERE ai_provider IS NULL;
