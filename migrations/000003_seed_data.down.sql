-- Migration: 000003_seed_data (DOWN)
-- Description: Remove seed data

-- Delete in reverse order of foreign key dependencies
DELETE FROM messages WHERE conversation_id = 'c0000000-0000-0000-0000-000000000001';
DELETE FROM conversations WHERE id = 'c0000000-0000-0000-0000-000000000001';
DELETE FROM crawl_logs WHERE source_type = 'seed';
DELETE FROM chunks WHERE document_id IN (
    'a0000000-0000-0000-0000-000000000001',
    'a0000000-0000-0000-0000-000000000002'
);
DELETE FROM documents WHERE id IN (
    'a0000000-0000-0000-0000-000000000001',
    'a0000000-0000-0000-0000-000000000002'
);
