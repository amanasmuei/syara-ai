# SyaRA - Shariah Regulatory AI Assistant

SyaRA (Shariah Regulatory Assistant) is an AI-powered platform that provides accurate, verifiable guidance on Shariah regulatory requirements for Islamic finance. Built with Retrieval-Augmented Generation (RAG) technology, SyaRA searches authoritative regulatory sources and delivers cited responses that professionals can trust and verify.

## What is SyaRA?

SyaRA addresses a critical challenge in Islamic finance: navigating the complex landscape of Shariah regulations across multiple jurisdictions and regulatory bodies. Instead of manually searching through hundreds of documents, users can ask questions in natural language and receive accurate, source-cited answers.

**The Problem:**
- Shariah regulations are scattered across multiple regulatory bodies (central banks, securities commissions, fiqh academies)
- Documents are often in PDF format, difficult to search
- Cross-referencing requirements across jurisdictions is time-consuming
- Risk of misinterpreting or missing critical regulatory requirements

**The Solution:**
SyaRA ingests, indexes, and semantically searches regulatory documents, then uses AI to synthesize accurate answers with precise citations back to the original sources.

## Key Features

### Multi-Source Regulatory Search
Search across comprehensive regulatory databases:

| Source | Description |
|--------|-------------|
| **BNM** | Bank Negara Malaysia regulations for Islamic banking |
| **SC Malaysia** | Securities Commission guidelines for Islamic capital markets |
| **AAOIFI** | International Shariah standards for Islamic financial institutions |
| **IIFA** | International Islamic Fiqh Academy (Majma Fiqh) resolutions |
| **State Fatwa** | Malaysian state fatwa committee rulings |

### Visual Citations
Every response includes citations that link directly to the source documents. The system highlights the exact passages in PDF documents, allowing users to verify information and explore context.

### Cross-Framework Comparison
Compare regulatory requirements across different frameworks. For example, compare BNM's Murabaha requirements with AAOIFI standards to understand both Malaysian and international compliance needs.

### Latest Regulatory Updates
Track recent circulars, guidelines, and regulatory changes across all indexed sources.

### Agentic AI Architecture
SyaRA uses an agentic approach where the AI:
1. Analyzes the user's question to determine the appropriate regulatory source
2. Searches relevant databases using specialized tools
3. Retrieves and ranks the most relevant document chunks
4. Synthesizes a comprehensive answer with proper citations
5. Can perform multi-step reasoning for complex queries

## How It Works

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         User Interface                          │
│                    (React Frontend / API)                       │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Agent Orchestrator                         │
│         (Manages conversation flow and tool selection)          │
└─────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┼───────────────┐
                ▼               ▼               ▼
        ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
        │ Search Tools│ │  Compare    │ │  Circulars  │
        │ BNM, AAOIFI │ │  Standards  │ │    Tool     │
        │ SC, IIFA... │ │    Tool     │ │             │
        └─────────────┘ └─────────────┘ └─────────────┘
                │               │               │
                └───────────────┼───────────────┘
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                       RAG Engine                                │
│    (Vector search, semantic retrieval, document ranking)        │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Vector Database                              │
│         (PostgreSQL + pgvector for embeddings)                  │
└─────────────────────────────────────────────────────────────────┘
```

### The RAG Pipeline

1. **Document Ingestion**
   - Crawlers fetch regulatory documents from official sources
   - PDFs are processed and text is extracted with layout preservation
   - Documents are chunked into semantic segments

2. **Embedding Generation**
   - Each chunk is converted to a vector embedding
   - Embeddings capture semantic meaning for similarity search
   - Metadata (source, date, category) is preserved

3. **Query Processing**
   - User questions are embedded using the same model
   - Vector similarity search finds relevant chunks
   - Results are ranked by relevance and recency

4. **Response Generation**
   - Retrieved chunks are provided as context to the LLM
   - The AI generates a response grounded in the sources
   - Citations are automatically linked to source documents

### Tool-Based Search

SyaRA uses specialized search tools for each regulatory source:

```
User: "What are the Murabaha requirements under BNM?"
       │
       ▼
Agent analyzes query → Selects search_bnm_regulations tool
       │
       ▼
Tool searches BNM documents in vector DB
       │
       ▼
Returns top-k relevant chunks with metadata
       │
       ▼
LLM synthesizes answer with [Source N] citations
```

## Target Users

- **Shariah Scholars & Advisors** - Research fiqh rulings and standards
- **Compliance Officers** - Verify regulatory requirements
- **Legal Professionals** - Cross-reference regulatory frameworks
- **Product Managers** - Understand Shariah requirements for new products
- **Regulators** - Compare international standards
- **Researchers & Academics** - Study Islamic finance regulations

## Project Structure

```
islamic-banking-agent/
├── cmd/
│   ├── agent/           # Main API server
│   ├── crawler/         # Data ingestion CLI
│   ├── worker/          # Background workers
│   └── image-processor/ # Visual citation processing
├── internal/
│   ├── agent/           # AI orchestration & system prompt
│   ├── api/             # HTTP handlers & middleware
│   ├── config/          # Configuration management
│   ├── crawler/         # Web crawling & PDF processing
│   ├── embedder/        # Embedding generation
│   ├── llm/             # LLM provider integrations
│   ├── rag/             # RAG engine & retrieval
│   ├── storage/         # Database & object storage
│   ├── tools/           # Agent search tools
│   ├── visual/          # Image processing & highlights
│   └── realtime/        # WebSocket & real-time updates
├── web/frontend/        # React TypeScript UI
├── migrations/          # Database schemas
└── docs/                # Documentation
```

## Prerequisites

- Go 1.22+
- Node.js 18+ (for frontend)
- Docker & Docker Compose
- PostgreSQL 16+ with pgvector extension
- Redis 7+
- NATS with JetStream
- MinIO (or S3-compatible storage)

## Quick Start

1. **Clone the repository**
   ```bash
   git clone https://github.com/alqutdigital/islamic-banking-agent.git
   cd islamic-banking-agent
   ```

2. **Configure environment**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys and configuration
   ```

3. **Start infrastructure**
   ```bash
   make docker-up
   ```

4. **Run migrations**
   ```bash
   make migrate
   ```

5. **Start the server**
   ```bash
   make run-agent
   ```

6. **Start the frontend** (in a separate terminal)
   ```bash
   cd web/frontend
   npm install
   npm run dev
   ```

## Data Ingestion (Crawling)

Before SyaRA can answer questions, you need to ingest regulatory documents into the vector database. The crawler CLI handles document fetching, PDF processing, chunking, and embedding generation.

### Build the Crawler

```bash
make build-crawler
```

### Ingest from Regulatory Sources

```bash
# Ingest BNM (Bank Negara Malaysia) policy documents
./bin/crawler ingest --source=bnm --category=policy-documents

# Ingest BNM circulars
./bin/crawler ingest --source=bnm --category=circulars

# Ingest Securities Commission Malaysia Shariah resolutions
./bin/crawler ingest --source=sc --category=shariah_resolutions

# Ingest AAOIFI Shariah standards
./bin/crawler ingest --source=aaoifi

# Ingest IIFA (Majma Fiqh) resolutions
./bin/crawler ingest --source=iifa

# Ingest Malaysian state fatwa (specific state)
./bin/crawler ingest --source=fatwa --state=selangor

# Ingest all Malaysian state fatwas
./bin/crawler ingest --source=fatwa --state=all
```

### Available Sources

| Source | Flag | Description |
|--------|------|-------------|
| BNM | `--source=bnm` | Bank Negara Malaysia regulations |
| SC Malaysia | `--source=sc` | Securities Commission guidelines |
| AAOIFI | `--source=aaoifi` | International Shariah standards |
| IIFA | `--source=iifa` | Islamic Fiqh Academy resolutions |
| State Fatwa | `--source=fatwa --state=<state>` | Malaysian state fatwa rulings |

### Additional Crawler Options

```bash
# Dry run (preview without saving)
./bin/crawler ingest --source=bnm --dry-run

# Force re-processing of existing documents
./bin/crawler ingest --source=bnm --force

# Process with more workers (faster)
./bin/crawler ingest --source=bnm --workers=8

# Skip embedding generation (process only)
./bin/crawler ingest --source=bnm --skip-embed

# Process a single local PDF file
./bin/crawler ingest --input=/path/to/document.pdf

# Process from a specific URL
./bin/crawler ingest --url=https://example.com/document.pdf
```

### Other Crawler Commands

```bash
# Check ingestion status
./bin/crawler status
./bin/crawler status --source=bnm

# Generate embeddings for existing documents
./bin/crawler embed --source=bnm

# Process a single PDF file
./bin/crawler process /path/to/file.pdf
```

## Configuration

Key environment variables:

| Variable | Description |
|----------|-------------|
| `LLM_PROVIDER` | LLM provider (anthropic, openai) |
| `ANTHROPIC_API_KEY` | Anthropic API key for Claude |
| `OPENAI_API_KEY` | OpenAI API key (for embeddings) |
| `LLM_MODEL` | Model to use (e.g., claude-sonnet-4-20250514) |
| `EMBEDDING_MODEL` | Embedding model (e.g., text-embedding-3-small) |
| `DB_HOST` | PostgreSQL host |
| `REDIS_HOST` | Redis host |

See `.env.example` for all configuration options.

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/api/v1/chat` | POST | Send a message to SyaRA |
| `/api/v1/conversations` | GET | List conversations |
| `/api/v1/documents/:id/download` | GET | Download source document |

## License

Copyright 2026 KoolekLabs. All rights reserved.
