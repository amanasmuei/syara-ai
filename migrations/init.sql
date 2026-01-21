-- Initialize database with pgvector extension
-- This script runs on first PostgreSQL startup

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create application user (optional, for production)
-- DO $$
-- BEGIN
--     IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'app_user') THEN
--         CREATE ROLE app_user WITH LOGIN PASSWORD 'app_password';
--     END IF;
-- END
-- $$;

-- Grant permissions (uncomment for production)
-- GRANT ALL PRIVILEGES ON DATABASE islamic_banking TO app_user;

-- Log successful initialization
DO $$
BEGIN
    RAISE NOTICE 'Database initialized successfully with pgvector extension';
END
$$;
