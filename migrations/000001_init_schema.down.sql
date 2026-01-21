-- Migration: 000001_init_schema (DOWN)
-- Description: Rollback initial schema

-- Drop functions first (due to dependencies)
DROP FUNCTION IF EXISTS search_chunks;
DROP FUNCTION IF EXISTS update_updated_at_column CASCADE;

-- Drop tables in reverse order of creation (respecting foreign keys)
DROP TABLE IF EXISTS search_analytics;
DROP TABLE IF EXISTS crawl_logs;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS chunk_visuals;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;

-- Note: We don't drop the extensions as they might be used by other schemas
-- DROP EXTENSION IF EXISTS vector;
-- DROP EXTENSION IF EXISTS "uuid-ossp";
