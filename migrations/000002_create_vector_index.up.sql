-- Migration: 000002_create_vector_index
-- Description: Create IVFFlat index for vector similarity search
-- Note: This should be run AFTER initial data ingestion for optimal index quality

-- Create IVFFlat index for cosine similarity search
-- lists = 100 is good for datasets up to ~100k vectors
-- Increase lists for larger datasets (sqrt(n) is a good rule of thumb)
CREATE INDEX IF NOT EXISTS idx_chunks_embedding
ON chunks USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);

-- Alternative: HNSW index for better accuracy (comment out IVFFlat if using this)
-- HNSW is slower to build but provides better recall
-- CREATE INDEX IF NOT EXISTS idx_chunks_embedding_hnsw
-- ON chunks USING hnsw (embedding vector_cosine_ops)
-- WITH (m = 16, ef_construction = 64);

COMMENT ON INDEX idx_chunks_embedding IS 'IVFFlat index for fast approximate nearest neighbor search';
