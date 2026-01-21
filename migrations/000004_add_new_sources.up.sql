-- Migration: 000004_add_new_sources
-- Description: Add new Shariah standards sources (SC, IIFA, State Fatwa)

-- =====================================================
-- UPDATE SOURCE_TYPE CHECK CONSTRAINT
-- =====================================================

-- Drop the existing CHECK constraint
ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_source_type_check;

-- Add new CHECK constraint with all source types
ALTER TABLE documents ADD CONSTRAINT documents_source_type_check
  CHECK (source_type IN (
    -- Existing sources
    'bnm',
    'aaoifi',
    'other',
    -- New sources
    'sc',           -- Securities Commission Malaysia
    'iifa',         -- International Islamic Fiqh Academy (Majma Fiqh)
    -- State Fatwa Authorities (All 13 states + Federal Territory)
    'fatwa_selangor',
    'fatwa_johor',
    'fatwa_penang',
    'fatwa_federal',
    'fatwa_perak',
    'fatwa_kedah',
    'fatwa_kelantan',
    'fatwa_terengganu',
    'fatwa_pahang',
    'fatwa_nsembilan',
    'fatwa_melaka',
    'fatwa_perlis',
    'fatwa_sabah',
    'fatwa_sarawak'
  ));

-- =====================================================
-- ADD NEW COLUMNS
-- =====================================================

-- Resolution number for IIFA resolutions (e.g., "Resolution No. 267")
ALTER TABLE documents ADD COLUMN IF NOT EXISTS resolution_number VARCHAR(100);

-- State identifier for fatwa sources
ALTER TABLE documents ADD COLUMN IF NOT EXISTS fatwa_state VARCHAR(50);

-- Document type for categorization (acts, guidelines, resolutions, etc.)
ALTER TABLE documents ADD COLUMN IF NOT EXISTS document_type VARCHAR(100);

-- =====================================================
-- ADD INDEXES FOR NEW SOURCE TYPES
-- =====================================================

-- Partial indexes for efficient filtering by source type
CREATE INDEX IF NOT EXISTS idx_documents_source_type_sc
  ON documents(source_type) WHERE source_type = 'sc';

CREATE INDEX IF NOT EXISTS idx_documents_source_type_iifa
  ON documents(source_type) WHERE source_type = 'iifa';

-- Index for all fatwa sources using pattern matching
CREATE INDEX IF NOT EXISTS idx_documents_source_type_fatwa
  ON documents(source_type) WHERE source_type LIKE 'fatwa_%';

-- Indexes for new columns
CREATE INDEX IF NOT EXISTS idx_documents_resolution_number
  ON documents(resolution_number) WHERE resolution_number IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_documents_fatwa_state
  ON documents(fatwa_state) WHERE fatwa_state IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_documents_document_type
  ON documents(document_type) WHERE document_type IS NOT NULL;

-- =====================================================
-- UPDATE SEARCH FUNCTION TO SUPPORT NEW SOURCES
-- =====================================================

-- Drop and recreate the search function to support new source types
DROP FUNCTION IF EXISTS search_chunks(vector(1536), FLOAT, INT, VARCHAR);

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
    document_title VARCHAR,
    standard_number VARCHAR,
    resolution_number VARCHAR,
    fatwa_state VARCHAR,
    document_type VARCHAR
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
        d.title AS document_title,
        d.standard_number,
        d.resolution_number,
        d.fatwa_state,
        d.document_type
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
-- ADD COMMENTS
-- =====================================================

COMMENT ON COLUMN documents.resolution_number IS 'Resolution number for IIFA/Majma Fiqh resolutions (e.g., Resolution No. 267)';
COMMENT ON COLUMN documents.fatwa_state IS 'Malaysian state for fatwa sources (selangor, johor, penang, etc.)';
COMMENT ON COLUMN documents.document_type IS 'Type of document (acts, guidelines, resolutions, circulars, etc.)';
