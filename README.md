# Islamic Banking Agent

ShariaComply AI - An intelligent compliance assistant for Islamic banking professionals powered by RAG (Retrieval-Augmented Generation) and agentic AI.

## Project Structure

```
islamic-banking-agent/
├── cmd/
│   ├── agent/           # Main API server
│   ├── crawler/         # Data ingestion CLI
│   ├── worker/          # Background workers
│   └── image-processor/ # Visual processing service
├── internal/
│   ├── config/          # Configuration management
│   ├── crawler/         # Web crawling & PDF processing
│   ├── chunker/         # Text chunking logic
│   ├── embedder/        # Embedding generation
│   ├── storage/         # Database & object storage
│   ├── rag/             # RAG engine & retrieval
│   ├── agent/           # AI agent orchestration
│   ├── tools/           # Agent tools
│   ├── visual/          # Image processing & highlights
│   ├── realtime/        # Real-time update system
│   └── api/             # HTTP handlers & middleware
├── pkg/
│   ├── logger/          # Structured logging
│   ├── shutdown/        # Graceful shutdown
│   └── validator/       # Input validation
├── web/                 # Frontend (React)
├── scripts/             # Build & deployment scripts
├── migrations/          # Database migrations
├── docker/              # Dockerfiles
└── docs/                # Documentation
```

## Prerequisites

- Go 1.22+
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

2. **Copy environment variables**
   ```bash
   cp .env.example .env
   # Edit .env with your configuration
   ```

3. **Start infrastructure services**
   ```bash
   make docker-up
   ```

4. **Run database migrations**
   ```bash
   make migrate
   ```

5. **Start the agent server**
   ```bash
   make run-agent
   ```

## Available Commands

```bash
# Build all binaries
make build

# Run tests
make test

# Run linter
make lint

# Start Docker services
make docker-up

# Stop Docker services
make docker-down

# Run database migrations
make migrate

# View all available commands
make help
```

## Services

| Service | Port | Description |
|---------|------|-------------|
| Agent API | 8080 | Main REST API server |
| Image Processor | 8081 | Visual citation processing |
| PostgreSQL | 5432 | Primary database |
| Redis | 6379 | Caching & sessions |
| NATS | 4222 | Message queue |
| MinIO | 9000 | Object storage |
| MinIO Console | 9001 | MinIO web UI |

## API Endpoints

### Health & Status
- `GET /health` - Health check
- `GET /ready` - Readiness check

### API v1
- `GET /api/v1/` - API information
- `POST /api/v1/chat` - Chat with the agent
- `GET /api/v1/conversations` - List conversations
- `GET /api/v1/documents/:id/download` - Download document

## Configuration

Configuration is loaded from environment variables. See `.env.example` for all available options.

### Key Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| PORT | HTTP server port | 8080 |
| ENVIRONMENT | Environment (development/production) | development |
| DB_HOST | PostgreSQL host | localhost |
| REDIS_HOST | Redis host | localhost |
| NATS_URL | NATS connection URL | nats://localhost:4222 |
| ANTHROPIC_API_KEY | Anthropic API key | - |
| OPENAI_API_KEY | OpenAI API key (for embeddings) | - |

## Development

### Running Tests
```bash
# All tests
make test

# With coverage
make coverage

# Integration tests
make test-integration
```

### Code Quality
```bash
# Run linter
make lint

# Fix linting issues
make lint-fix

# Format code
make fmt
```

## License

Copyright © 2024 Alqut Digital. All rights reserved.
