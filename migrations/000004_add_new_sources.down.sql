-- Migration: 000004_add_new_sources (DOWN)
-- Description: Revert new Shariah standards sources

-- =====================================================
-- DROP NEW INDEXES
-- =====================================================

DROP INDEX IF EXISTS idx_documents_source_type_sc;
DROP INDEX IF EXISTS idx_documents_source_type_iifa;
DROP INDEX IF EXISTS idx_documents_source_type_fatwa;
DROP INDEX IF EXISTS idx_documents_resolution_number;
DROP INDEX IF EXISTS idx_documents_fatwa_state;
DROP INDEX IF EXISTS idx_documents_document_type;

-- =====================================================
-- DROP NEW COLUMNS
-- =====================================================

ALTER TABLE documents DROP COLUMN IF EXISTS resolution_number;
ALTER TABLE documents DROP COLUMN IF EXISTS fatwa_state;
ALTER TABLE documents DROP COLUMN IF EXISTS document_type;

-- =====================================================
-- RESTORE ORIGINAL CHECK CONSTRAINT
-- =====================================================

ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_source_type_check;
ALTER TABLE documents ADD CONSTRAINT documents_source_type_check
  CHECK (source_type IN ('bnm', 'aaoifi', 'other'));

-- =====================================================
-- RESTORE ORIGINAL SEARCH FUNCTION
-- =====================================================

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
