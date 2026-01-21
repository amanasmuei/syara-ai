-- Migration: 000002_create_vector_index (DOWN)
-- Description: Drop vector index

DROP INDEX CONCURRENTLY IF EXISTS idx_chunks_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_chunks_embedding_hnsw;
