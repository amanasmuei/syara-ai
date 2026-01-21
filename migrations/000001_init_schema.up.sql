-- Migration: 000001_init_schema
-- Description: Initialize database schema with pgvector for Islamic Banking Agent

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- =====================================================
-- DOCUMENTS TABLE
-- Stores original document metadata (PDFs, web pages)
-- =====================================================
CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type VARCHAR(50) NOT NULL CHECK (source_type IN ('bnm', 'aaoifi', 'other')),
    title VARCHAR(500) NOT NULL,
    file_name VARCHAR(255),
    file_path TEXT,
    original_url TEXT,
    category VARCHAR(100),
    standard_number VARCHAR(50),
    effective_date DATE,
    content_hash VARCHAR(64) UNIQUE,
    total_pages INT,
    language VARCHAR(10) DEFAULT 'en',
    metadata JSONB DEFAULT '{}',
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Document indexes
CREATE INDEX idx_documents_source_type ON documents(source_type);
CREATE INDEX idx_documents_category ON documents(category);
CREATE INDEX idx_documents_standard_number ON documents(standard_number);
CREATE INDEX idx_documents_content_hash ON documents(content_hash);
CREATE INDEX idx_documents_is_active ON documents(is_active);
CREATE INDEX idx_documents_created_at ON documents(created_at DESC);

-- =====================================================
-- CHUNKS TABLE
-- Stores text chunks with vector embeddings
-- =====================================================
CREATE TABLE chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    embedding vector(1536), -- OpenAI text-embedding-3-small dimension
    page_number INT,
    section_title VARCHAR(500),
    section_hierarchy TEXT[], -- Array of parent section titles
    chunk_index INT NOT NULL,
    start_char INT,
    end_char INT,
    token_count INT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Chunk indexes
CREATE INDEX idx_chunks_document_id ON chunks(document_id);
CREATE INDEX idx_chunks_page_number ON chunks(page_number);
CREATE INDEX idx_chunks_chunk_index ON chunks(document_id, chunk_index);

-- Vector similarity search index (IVFFlat for performance)
-- Note: Run this AFTER inserting initial data for better index quality
-- CREATE INDEX idx_chunks_embedding ON chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- =====================================================
-- CHUNK_VISUALS TABLE
-- Stores visual metadata for citations (bounding boxes, images)
-- =====================================================
CREATE TABLE chunk_visuals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chunk_id UUID NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    page_image_path TEXT,
    thumbnail_path TEXT,
    highlighted_image_path TEXT,
    bounding_box JSONB, -- {x, y, width, height, page_width, page_height}
    text_coordinates JSONB[], -- Array of text line coordinates
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Chunk visuals indexes
CREATE INDEX idx_chunk_visuals_chunk_id ON chunk_visuals(chunk_id);

-- =====================================================
-- CONVERSATIONS TABLE
-- Stores chat conversations
-- =====================================================
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID,
    title VARCHAR(255),
    summary TEXT,
    metadata JSONB DEFAULT '{}',
    is_archived BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Conversation indexes
CREATE INDEX idx_conversations_user_id ON conversations(user_id);
CREATE INDEX idx_conversations_created_at ON conversations(created_at DESC);
CREATE INDEX idx_conversations_is_archived ON conversations(is_archived);

-- =====================================================
-- MESSAGES TABLE
-- Stores chat messages within conversations
-- =====================================================
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    citations JSONB DEFAULT '[]',
    tool_calls JSONB DEFAULT '[]',
    tool_results JSONB DEFAULT '[]',
    tokens_used INT,
    model_used VARCHAR(100),
    latency_ms INT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Message indexes
CREATE INDEX idx_messages_conversation_id ON messages(conversation_id);
CREATE INDEX idx_messages_created_at ON messages(created_at);
CREATE INDEX idx_messages_role ON messages(role);

-- =====================================================
-- CRAWL_LOGS TABLE
-- Tracks crawling/ingestion history
-- =====================================================
CREATE TABLE crawl_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type VARCHAR(50) NOT NULL,
    url TEXT,
    status VARCHAR(20) NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    documents_found INT DEFAULT 0,
    documents_processed INT DEFAULT 0,
    chunks_created INT DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Crawl log indexes
CREATE INDEX idx_crawl_logs_source_type ON crawl_logs(source_type);
CREATE INDEX idx_crawl_logs_status ON crawl_logs(status);
CREATE INDEX idx_crawl_logs_created_at ON crawl_logs(created_at DESC);

-- =====================================================
-- SEARCH_ANALYTICS TABLE
-- Tracks search queries for analytics and improvement
-- =====================================================
CREATE TABLE search_analytics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query TEXT NOT NULL,
    query_embedding vector(1536),
    results_count INT,
    top_document_ids UUID[],
    user_id UUID,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    latency_ms INT,
    feedback_score INT CHECK (feedback_score BETWEEN 1 AND 5),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Search analytics indexes
CREATE INDEX idx_search_analytics_created_at ON search_analytics(created_at DESC);
CREATE INDEX idx_search_analytics_user_id ON search_analytics(user_id);

-- =====================================================
-- FUNCTIONS
-- =====================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply updated_at trigger to relevant tables
CREATE TRIGGER update_documents_updated_at
    BEFORE UPDATE ON documents
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_conversations_updated_at
    BEFORE UPDATE ON conversations
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Function for semantic search
CREATE OR REPLACE FUNCTION search_chunks(
    query_embedding vector(1536),
    match_threshold FLOAT DEFAULT 0.7,
    match_count INT DEFAULT 5,
    filter_source_type VARCHAR DEFAULT NULL
)
RETURNS TABLE (
    chunk_id UUID,
    document_id UUID,
    content TEXT,
    similarity FLOAT,
    page_number INT,
    section_title VARCHAR,
    source_type VARCHAR,
    document_title VARCHAR
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        c.id AS chunk_id,
        c.document_id,
        c.content,
        1 - (c.embedding <=> query_embedding) AS similarity,
        c.page_number,
        c.section_title,
        d.source_type,
        d.title AS document_title
    FROM chunks c
    JOIN documents d ON c.document_id = d.id
    WHERE
        d.is_active = true
        AND c.embedding IS NOT NULL
        AND (filter_source_type IS NULL OR d.source_type = filter_source_type)
        AND 1 - (c.embedding <=> query_embedding) > match_threshold
    ORDER BY c.embedding <=> query_embedding
    LIMIT match_count;
END;
$$ LANGUAGE plpgsql;

-- =====================================================
-- COMMENTS
-- =====================================================
COMMENT ON TABLE documents IS 'Stores metadata for source documents (PDFs, web pages)';
COMMENT ON TABLE chunks IS 'Stores text chunks with vector embeddings for RAG';
COMMENT ON TABLE chunk_visuals IS 'Stores visual citation data (bounding boxes, images)';
COMMENT ON TABLE conversations IS 'Stores chat conversation sessions';
COMMENT ON TABLE messages IS 'Stores individual messages within conversations';
COMMENT ON TABLE crawl_logs IS 'Tracks document crawling and ingestion history';
COMMENT ON TABLE search_analytics IS 'Tracks search queries for analytics';

COMMENT ON COLUMN chunks.embedding IS 'Vector embedding from text-embedding-3-small (1536 dimensions)';
COMMENT ON COLUMN chunk_visuals.bounding_box IS 'JSON with x, y, width, height, page_width, page_height';
