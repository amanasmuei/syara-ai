.PHONY: all build test lint clean run-agent run-crawler run-worker docker-up docker-down migrate help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Binary names
AGENT_BINARY=bin/agent
CRAWLER_BINARY=bin/crawler
WORKER_BINARY=bin/worker
IMAGE_PROCESSOR_BINARY=bin/image-processor

# Build flags
BUILD_FLAGS=-ldflags="-s -w"

all: lint test build

## Build commands
build: build-agent build-crawler build-worker build-image-processor

build-agent:
	@echo "Building agent..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(AGENT_BINARY) ./cmd/agent

build-crawler:
	@echo "Building crawler..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(CRAWLER_BINARY) ./cmd/crawler

build-worker:
	@echo "Building worker..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(WORKER_BINARY) ./cmd/worker

build-image-processor:
	@echo "Building image processor..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(IMAGE_PROCESSOR_BINARY) ./cmd/image-processor

## Run commands
run-agent:
	@echo "Running agent..."
	$(GOCMD) run ./cmd/agent

run-crawler:
	@echo "Running crawler..."
	$(GOCMD) run ./cmd/crawler

run-worker:
	@echo "Running worker..."
	$(GOCMD) run ./cmd/worker

## Test commands
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

test-short:
	@echo "Running short tests..."
	$(GOTEST) -v -short ./...

test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -tags=integration ./test/integration/...

coverage:
	@echo "Generating coverage report..."
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

## Lint commands
lint:
	@echo "Running linter..."
	$(GOLINT) run ./...

lint-fix:
	@echo "Running linter with fix..."
	$(GOLINT) run --fix ./...

fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

## Dependency commands
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

## Docker commands
docker-up:
	@echo "Starting Docker services..."
	docker-compose up -d

docker-down:
	@echo "Stopping Docker services..."
	docker-compose down

docker-logs:
	@echo "Showing Docker logs..."
	docker-compose logs -f

docker-build:
	@echo "Building Docker images..."
	docker-compose build

## Database commands
migrate:
	@echo "Running migrations..."
	migrate -path ./migrations -database "$${DATABASE_URL}" up

migrate-down:
	@echo "Rolling back migrations..."
	migrate -path ./migrations -database "$${DATABASE_URL}" down 1

migrate-create:
	@echo "Creating new migration..."
	@read -p "Enter migration name: " name; \
	migrate create -ext sql -dir ./migrations -seq $$name

## Clean commands
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f coverage.out coverage.html

## Help
help:
	@echo "Available targets:"
	@echo "  build              - Build all binaries"
	@echo "  build-agent        - Build agent binary"
	@echo "  build-crawler      - Build crawler binary"
	@echo "  build-worker       - Build worker binary"
	@echo "  run-agent          - Run agent server"
	@echo "  run-crawler        - Run crawler CLI"
	@echo "  run-worker         - Run background worker"
	@echo "  test               - Run all tests"
	@echo "  test-short         - Run short tests only"
	@echo "  test-integration   - Run integration tests"
	@echo "  coverage           - Generate coverage report"
	@echo "  lint               - Run linter"
	@echo "  lint-fix           - Run linter with auto-fix"
	@echo "  fmt                - Format code"
	@echo "  deps               - Download dependencies"
	@echo "  tidy               - Tidy dependencies"
	@echo "  docker-up          - Start Docker services"
	@echo "  docker-down        - Stop Docker services"
	@echo "  docker-logs        - Show Docker logs"
	@echo "  migrate            - Run database migrations"
	@echo "  migrate-down       - Rollback last migration"
	@echo "  migrate-create     - Create new migration"
	@echo "  clean              - Remove build artifacts"
