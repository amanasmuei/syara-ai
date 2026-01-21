# Developer Onboarding Guide

This guide helps you get started with developing the ShariaComply AI Islamic Banking Agent.

## Prerequisites

### Required Software

- **Go 1.22+**: [Download](https://go.dev/dl/)
- **Node.js 20+**: [Download](https://nodejs.org/)
- **Docker & Docker Compose**: [Download](https://www.docker.com/)
- **PostgreSQL client** (optional): `brew install postgresql` or `apt install postgresql-client`

### API Keys

You'll need the following API keys:
- **Anthropic API Key**: For Claude LLM ([Get here](https://console.anthropic.com/))
- **OpenAI API Key**: For embeddings ([Get here](https://platform.openai.com/))

## Quick Start

### 1. Clone and Setup

```bash
git clone <repository-url>
cd islamic-banking-agent

# Copy environment file
cp .env.example .env
# Edit .env with your API keys
```

### 2. Start Infrastructure

```bash
# Start PostgreSQL, Redis, NATS, MinIO
docker-compose up -d

# Verify services are running
docker-compose ps
```

### 3. Run Database Migrations

```bash
# Install migrate tool
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Run migrations
export DATABASE_URL="postgres://postgres:devpassword@localhost:5432/islamic_banking?sslmode=disable"
migrate -path ./migrations -database "$DATABASE_URL" up
```

### 4. Build and Run

```bash
# Download dependencies
make deps

# Build all binaries
make build

# Run the agent server
make run-agent
```

The API will be available at `http://localhost:8080`.

## Project Structure

```
islamic-banking-agent/
├── cmd/                    # Application entry points
│   ├── agent/              # Main API server
│   ├── crawler/            # Data ingestion CLI
│   ├── worker/             # Background worker
│   └── image-processor/    # Image processing service
├── internal/               # Private application code
│   ├── agent/              # AI agent orchestration
│   ├── api/                # HTTP handlers & middleware
│   ├── chunker/            # Text chunking
│   ├── config/             # Configuration loading
│   ├── crawler/            # Web crawling
│   ├── embedder/           # Embedding generation
│   ├── processor/          # Document processing
│   ├── rag/                # RAG retrieval & context
│   ├── realtime/           # Real-time updates (NATS, WebSocket)
│   ├── storage/            # Database & cache
│   ├── tools/              # Agent tools
│   ├── testing/            # Test utilities
│   └── visual/             # Visual citations
├── pkg/                    # Public packages
│   └── logger/             # Structured logging
├── web/
│   └── frontend/           # React frontend
├── migrations/             # Database migrations
├── docker/                 # Dockerfiles
├── docs/                   # Documentation
└── test/                   # Integration tests
```

## Development Workflow

### Running Services Locally

```bash
# Terminal 1: API Server
make run-agent

# Terminal 2: Background Worker
make run-worker

# Terminal 3: Frontend (optional)
cd web/frontend && npm install && npm run dev
```

### Running Tests

```bash
# Run all tests
make test

# Run short tests (no external dependencies)
make test-short

# Run integration tests (requires Docker)
make test-integration

# Generate coverage report
make coverage
```

### Code Quality

```bash
# Run linter
make lint

# Run linter with auto-fix
make lint-fix

# Format code
make fmt
```

## Key Components

### 1. RAG Engine (`internal/rag/`)

The Retrieval-Augmented Generation engine handles:
- **Retriever**: Hybrid search combining semantic (vector) and keyword search
- **Context Builder**: Constructs LLM context from retrieved chunks
- **Query Processor**: Normalizes queries and extracts entities

```go
// Example: Using the retriever
results, err := retriever.Retrieve(ctx, query, RetrievalOptions{
    TopK:       10,
    MinScore:   0.5,
    SearchType: HybridSearch,
})
```

### 2. Agent Orchestrator (`internal/agent/`)

Manages AI agent interactions with tool calling:
- Integrates with Claude API
- Manages conversation memory
- Executes tool calls in a loop

```go
// Example: Processing a chat message
response, err := orchestrator.Chat(ctx, ChatRequest{
    ConversationID: uuid,
    Message:        "What is Murabaha?",
})
```

### 3. Visual Citations (`internal/visual/`)

Creates visual evidence for citations:
- **Highlighter**: Draws highlight regions on page images
- **Cropper**: Crops images with context padding
- **PDF Extractor**: Extracts single pages from PDFs

### 4. Real-Time Updates (`internal/realtime/`)

Handles live content updates:
- **NATS Client**: Event streaming via JetStream
- **Change Detector**: Monitors BNM for content changes
- **WebSocket Hub**: Pushes updates to clients

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_NAME` | Database name | `islamic_banking` |
| `REDIS_HOST` | Redis host | `localhost` |
| `NATS_URL` | NATS connection URL | `nats://localhost:4222` |
| `ANTHROPIC_API_KEY` | Claude API key | Required |
| `OPENAI_API_KEY` | OpenAI API key | Required |
| `LLM_MODEL` | LLM model to use | `claude-sonnet-4-20250514` |
| `EMBEDDING_MODEL` | Embedding model | `text-embedding-3-small` |

See `.env.example` for the complete list.

## Database Schema

Key tables:
- `documents`: Source documents (BNM, AAOIFI)
- `chunks`: Text chunks with embeddings
- `chunk_visuals`: Visual citation data
- `conversations`: Chat conversations
- `messages`: Chat messages with citations

Run `\d+ <table>` in psql for detailed schemas.

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check |
| `/api/v1/chat` | POST | Chat with AI |
| `/api/v1/conversations` | GET/POST | List/create conversations |
| `/api/v1/conversations/{id}` | GET/PUT/DELETE | Manage conversation |
| `/api/v1/documents/{id}` | GET | Get document metadata |
| `/api/v1/documents/{id}/download` | GET | Download document |

See `docs/api/openapi.yaml` for complete API documentation.

## Troubleshooting

### Database connection issues
```bash
# Check PostgreSQL is running
docker-compose ps postgres

# Test connection
psql "postgres://postgres:devpassword@localhost:5432/islamic_banking?sslmode=disable"
```

### NATS connection issues
```bash
# Check NATS is running
docker-compose ps nats

# View NATS logs
docker-compose logs nats
```

### Build errors
```bash
# Clean and rebuild
make clean
go mod tidy
make build
```

## Contributing

1. Create a feature branch from `develop`
2. Make changes with tests
3. Run `make lint test`
4. Submit a pull request

## Getting Help

- Check existing documentation in `/docs`
- Review API spec at `docs/api/openapi.yaml`
- Check Linear issues for known bugs
