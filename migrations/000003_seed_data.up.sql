-- Migration: 000003_seed_data
-- Description: Seed initial sample data for development/testing

-- Insert sample BNM document
INSERT INTO documents (
    id,
    source_type,
    title,
    file_name,
    category,
    standard_number,
    metadata
) VALUES (
    'a0000000-0000-0000-0000-000000000001',
    'bnm',
    'Murabahah Policy Document',
    'murabahah-policy-2023.pdf',
    'Islamic Banking',
    'BNM/RH/PD 028-9',
    '{"version": "2023", "issuer": "Bank Negara Malaysia", "type": "Policy Document"}'
);

-- Insert sample AAOIFI document
INSERT INTO documents (
    id,
    source_type,
    title,
    file_name,
    category,
    standard_number,
    metadata
) VALUES (
    'a0000000-0000-0000-0000-000000000002',
    'aaoifi',
    'Shariah Standard No. 8: Murabahah',
    'aaoifi-ss-8.pdf',
    'Shariah Standards',
    'SS 8',
    '{"version": "2017", "issuer": "AAOIFI", "type": "Shariah Standard"}'
);

-- Insert sample chunks for BNM document (without embeddings - these would be generated)
INSERT INTO chunks (
    id,
    document_id,
    content,
    page_number,
    section_title,
    chunk_index,
    token_count,
    metadata
) VALUES
(
    'b0000000-0000-0000-0000-000000000001',
    'a0000000-0000-0000-0000-000000000001',
    'Murabahah is a sale contract whereby the Institution sells to a customer a specified kind of asset that is already in its possession, whereby the selling price is the sum of the original price and an agreed profit margin.',
    1,
    'Definition of Murabahah',
    0,
    45,
    '{"section_number": "1.1"}'
),
(
    'b0000000-0000-0000-0000-000000000002',
    'a0000000-0000-0000-0000-000000000001',
    'The Institution must own the asset before selling it to the customer. The ownership must be genuine and not merely constructive. The Institution bears the risk of the asset from the time of purchase until delivery to the customer.',
    2,
    'Ownership Requirements',
    1,
    48,
    '{"section_number": "2.1"}'
);

-- Insert sample chunks for AAOIFI document
INSERT INTO chunks (
    id,
    document_id,
    content,
    page_number,
    section_title,
    chunk_index,
    token_count,
    metadata
) VALUES
(
    'b0000000-0000-0000-0000-000000000003',
    'a0000000-0000-0000-0000-000000000002',
    'Murabahah to the purchase orderer is a sale transaction where the institution purchases an asset based on the promise from the customer to buy it. The institution then sells it to the customer at cost plus profit.',
    45,
    'Definition',
    0,
    42,
    '{"section_number": "3.1"}'
),
(
    'b0000000-0000-0000-0000-000000000004',
    'a0000000-0000-0000-0000-000000000002',
    'It is not permissible for the institution to sell the asset to the customer before taking possession of it. The possession may be actual or constructive as recognized by Shariah.',
    46,
    'Possession Requirements',
    1,
    38,
    '{"section_number": "3.2"}'
);

-- Insert sample conversation
INSERT INTO conversations (
    id,
    title,
    metadata
) VALUES (
    'c0000000-0000-0000-0000-000000000001',
    'Murabahah Requirements Discussion',
    '{"tags": ["murabahah", "compliance"]}'
);

-- Insert sample messages
INSERT INTO messages (
    conversation_id,
    role,
    content,
    citations
) VALUES
(
    'c0000000-0000-0000-0000-000000000001',
    'user',
    'What are the ownership requirements for Murabahah under BNM regulations?',
    '[]'
),
(
    'c0000000-0000-0000-0000-000000000001',
    'assistant',
    'According to BNM Policy Document BNM/RH/PD 028-9, the Institution must own the asset before selling it to the customer. The ownership must be genuine and not merely constructive. The Institution bears the risk of the asset from the time of purchase until delivery to the customer. [1]

This is also consistent with AAOIFI Shariah Standard No. 8, which states that it is not permissible for the institution to sell the asset to the customer before taking possession of it. [2]',
    '[{"index": 1, "chunk_id": "b0000000-0000-0000-0000-000000000002", "source": "BNM Murabahah Policy", "page": 2}, {"index": 2, "chunk_id": "b0000000-0000-0000-0000-000000000004", "source": "AAOIFI SS 8", "page": 46}]'
);

-- Insert initial crawl log
INSERT INTO crawl_logs (
    source_type,
    status,
    documents_found,
    documents_processed,
    chunks_created,
    started_at,
    completed_at,
    metadata
) VALUES (
    'seed',
    'completed',
    2,
    2,
    4,
    NOW() - INTERVAL '1 minute',
    NOW(),
    '{"type": "seed_data", "environment": "development"}'
);
