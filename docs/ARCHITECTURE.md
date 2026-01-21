# System Architecture

## Overview

ShariaComply AI is a RAG (Retrieval-Augmented Generation) based compliance assistant for Islamic banking professionals. It provides AI-powered responses with visual citations from BNM regulations and AAOIFI Shariah Standards.

## High-Level Architecture

```mermaid
graph TB
    subgraph "Client Layer"
        WEB[React Frontend]
        API_CLIENT[API Clients]
    end

    subgraph "API Gateway"
        ROUTER[Chi Router]
        MW[Middleware Stack]
        RATE[Rate Limiter]
    end

    subgraph "Application Layer"
        AGENT[AI Agent Orchestrator]
        TOOLS[Agent Tools]
        RAG[RAG Engine]
        VISUAL[Visual Citations]
    end

    subgraph "Data Layer"
        PG[(PostgreSQL + pgvector)]
        REDIS[(Redis Cache)]
        MINIO[(MinIO/S3)]
    end

    subgraph "Messaging Layer"
        NATS[NATS JetStream]
        WS[WebSocket Hub]
    end

    subgraph "External Services"
        CLAUDE[Claude API]
        OPENAI[OpenAI Embeddings]
        BNM[BNM Website]
    end

    WEB --> ROUTER
    API_CLIENT --> ROUTER
    ROUTER --> MW
    MW --> RATE
    RATE --> AGENT

    AGENT --> TOOLS
    AGENT --> RAG
    AGENT --> CLAUDE

    TOOLS --> RAG
    RAG --> PG
    RAG --> REDIS
    RAG --> OPENAI

    VISUAL --> MINIO
    VISUAL --> PG

    NATS --> WS
    WS --> WEB

    BNM -.-> NATS
```

## Component Architecture

### 1. API Layer (`internal/api/`)

```mermaid
graph LR
    subgraph "API Layer"
        R[Router] --> MW[Middleware]
        MW --> H[Handlers]

        subgraph "Middleware"
            LOG[Logging]
            RATE[Rate Limit]
            CORS[CORS]
            REC[Recovery]
        end

        subgraph "Handlers"
            CHAT[Chat Handler]
            CONV[Conversation Handler]
            DOC[Document Handler]
            HEALTH[Health Handler]
        end
    end
```

**Responsibilities:**
- HTTP request routing via Chi router
- Request validation and error handling
- Rate limiting (per-endpoint limits)
- Structured logging and metrics
- CORS and security headers

### 2. AI Agent (`internal/agent/`)

```mermaid
sequenceDiagram
    participant U as User
    participant O as Orchestrator
    participant R as RAG Engine
    participant T as Tools
    participant C as Claude API

    U->>O: Chat Request
    O->>R: Initial Retrieval
    R-->>O: Context Chunks
    O->>C: Generate Response

    loop Tool Calls
        C-->>O: Tool Call Request
        O->>T: Execute Tool
        T-->>O: Tool Result
        O->>C: Continue with Result
    end

    C-->>O: Final Response
    O-->>U: Chat Response + Citations
```

**Components:**
- **Orchestrator**: Manages Claude API interaction with tool calling loop
- **Memory**: PostgreSQL + Redis conversation history
- **Tools**: BNM search, AAOIFI search, comparison, latest circulars

### 3. RAG Engine (`internal/rag/`)

```mermaid
graph TB
    subgraph "RAG Pipeline"
        QP[Query Processor] --> RET[Retriever]
        RET --> CB[Context Builder]

        subgraph "Retriever"
            VS[Vector Search]
            KS[Keyword Search]
            RRF[RRF Fusion]
        end

        VS --> RRF
        KS --> RRF
    end

    subgraph "Data Sources"
        PG[(PostgreSQL)]
        CACHE[(Redis Cache)]
    end

    QP --> CACHE
    RET --> PG
    RET --> CACHE
```

**Components:**
- **Query Processor**: Normalizes queries, extracts entities, suggests filters
- **Retriever**: Hybrid search with Reciprocal Rank Fusion
- **Context Builder**: Constructs LLM context with token budget management
- **Cache**: Redis caching for embeddings and retrieval results

### 4. Visual Citations (`internal/visual/`)

```mermaid
graph LR
    subgraph "Visual Pipeline"
        EXT[PDF Extractor] --> CROP[Cropper]
        CROP --> HIGH[Highlighter]
        HIGH --> BUILD[Citation Builder]
    end

    subgraph "Storage"
        MINIO[(MinIO/S3)]
    end

    BUILD --> MINIO
```

**Components:**
- **PDF Extractor**: Extracts single pages from PDFs using pdfcpu
- **Highlighter**: Draws bounding boxes on page images using gg
- **Cropper**: Crops images with context padding
- **Citation Builder**: Assembles visual citations with URLs

### 5. Real-Time Architecture (`internal/realtime/`)

```mermaid
graph TB
    subgraph "Event Sources"
        DET[Change Detector]
        CRAWL[Crawler Events]
    end

    subgraph "Message Broker"
        NATS[NATS JetStream]

        subgraph "Streams"
            DOC_STREAM[DOCUMENTS]
            SEARCH_STREAM[SEARCHES]
            UPDATE_STREAM[UPDATES]
        end
    end

    subgraph "Workers"
        CRAWL_W[Crawl Worker]
        PROC_W[Process Worker]
        INDEX_W[Index Worker]
    end

    subgraph "Clients"
        WS_HUB[WebSocket Hub]
        CACHE_INV[Cache Invalidator]
    end

    DET --> NATS
    CRAWL --> NATS

    NATS --> CRAWL_W
    NATS --> PROC_W
    NATS --> INDEX_W
    NATS --> WS_HUB
    NATS --> CACHE_INV
```

**Components:**
- **NATS Client**: JetStream with 3 streams (DOCUMENTS, SEARCHES, UPDATES)
- **Change Detector**: Monitors BNM for content changes
- **Worker Pool**: Background processing with manual acknowledgment
- **WebSocket Hub**: Pushes updates to connected clients
- **Cache Invalidator**: Clears stale cache entries

## Data Flow

### Chat Request Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant A as API
    participant AG as Agent
    participant RAG as RAG Engine
    participant DB as PostgreSQL
    participant LLM as Claude

    C->>A: POST /api/v1/chat
    A->>A: Validate Request
    A->>AG: Process Chat
    AG->>RAG: Retrieve Context
    RAG->>DB: Vector + Keyword Search
    DB-->>RAG: Chunks
    RAG-->>AG: Context
    AG->>LLM: Generate (with tools)
    LLM-->>AG: Response + Citations
    AG->>DB: Save Message
    AG-->>A: Chat Response
    A-->>C: JSON Response
```

### Document Ingestion Flow

```mermaid
sequenceDiagram
    participant CLI as Crawler CLI
    participant CR as Crawler
    participant PROC as Processor
    participant CHUNK as Chunker
    participant EMB as Embedder
    participant DB as PostgreSQL
    participant S3 as MinIO

    CLI->>CR: Start Ingestion
    CR->>CR: Crawl BNM/AAOIFI
    CR->>S3: Store PDF
    CR->>PROC: Process Document
    PROC->>PROC: Extract Text + Images
    PROC->>S3: Store Page Images
    PROC->>CHUNK: Chunk Text
    CHUNK->>EMB: Generate Embeddings
    EMB->>DB: Store Chunks + Vectors
```

## Database Schema

```mermaid
erDiagram
    documents {
        uuid id PK
        string source_type
        string title
        string file_name
        string file_path
        string original_url
        string category
        string standard_number
        date effective_date
        int total_pages
        string language
        bool is_active
        timestamp created_at
        timestamp updated_at
    }

    chunks {
        uuid id PK
        uuid document_id FK
        text content
        vector embedding
        int page_number
        string section_title
        text[] section_hierarchy
        int chunk_index
        int start_char
        int end_char
        int token_count
        jsonb metadata
        timestamp created_at
    }

    chunk_visuals {
        uuid id PK
        uuid chunk_id FK
        string page_image_path
        string thumbnail_path
        string highlighted_image_path
        jsonb bounding_box
        timestamp created_at
    }

    conversations {
        uuid id PK
        uuid user_id
        string title
        text summary
        bool is_archived
        int message_count
        timestamp created_at
        timestamp updated_at
    }

    messages {
        uuid id PK
        uuid conversation_id FK
        string role
        text content
        jsonb citations
        int tokens_used
        string model_used
        int latency_ms
        timestamp created_at
    }

    documents ||--o{ chunks : contains
    chunks ||--o| chunk_visuals : has
    conversations ||--o{ messages : contains
```

## Technology Stack

| Layer | Technology | Purpose |
|-------|------------|---------|
| **Frontend** | React 18, TypeScript, Tailwind CSS | User interface |
| **API** | Go 1.22+, Chi router | HTTP server |
| **AI** | Claude API (Anthropic) | LLM responses |
| **Embeddings** | OpenAI text-embedding-3-small | Vector embeddings |
| **Database** | PostgreSQL 16 + pgvector | Data + vector storage |
| **Cache** | Redis 7 | Caching + sessions |
| **Messaging** | NATS JetStream | Event streaming |
| **Object Storage** | MinIO (S3-compatible) | Files + images |
| **PDF Processing** | go-fitz (MuPDF), pdfcpu | PDF extraction |
| **Image Processing** | fogleman/gg | Image manipulation |

## Deployment Architecture

```mermaid
graph TB
    subgraph "Load Balancer"
        LB[Nginx/Traefik]
    end

    subgraph "Application Pods"
        API1[API Server 1]
        API2[API Server 2]
        WORKER1[Worker 1]
        WORKER2[Worker 2]
    end

    subgraph "Data Tier"
        PG[(PostgreSQL Primary)]
        PG_R[(PostgreSQL Replica)]
        REDIS[(Redis Cluster)]
        NATS[(NATS Cluster)]
        MINIO[(MinIO)]
    end

    LB --> API1
    LB --> API2

    API1 --> PG
    API2 --> PG
    WORKER1 --> NATS
    WORKER2 --> NATS

    PG --> PG_R
```

## Security Considerations

1. **API Security**: Rate limiting, input validation, CORS
2. **Data Security**: Encrypted connections (TLS), parameterized queries
3. **Secrets Management**: Environment variables, never in code
4. **Access Control**: Signed URLs for document downloads

## Performance Optimizations

1. **Caching**: Redis for embeddings (1hr TTL), retrieval results (5min TTL)
2. **Connection Pooling**: PostgreSQL (25 open, 5 idle)
3. **Batch Processing**: Embedding generation in batches of 100
4. **Vector Index**: IVFFlat with 100 lists for fast similarity search
5. **Worker Pools**: Concurrent document processing
