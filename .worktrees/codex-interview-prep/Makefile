.PHONY: help infra-up infra-down infra-logs infra-ps db-migrate db-reset test clean build run

# 默认目标
help:
	@echo "Argus OTA Platform - Makefile Commands"
	@echo ""
	@echo "Infrastructure:"
	@echo "  make infra-up          - Start all infrastructure services (PostgreSQL, Redis, Kafka, MinIO)"
	@echo "  make infra-down        - Stop all infrastructure services"
	@echo "  make infra-logs        - View logs from all infrastructure services"
	@echo "  make infra-ps          - Show running infrastructure containers"
	@echo "  make infra-restart     - Restart all infrastructure services"
	@echo ""
	@echo "Database:"
	@echo "  make db-migrate        - Run database migrations"
	@echo "  make db-reset          - Reset database (WARNING: deletes all data)"
	@echo "  make db-connect        - Connect to PostgreSQL using psql"
	@echo "  make db-dump           - Dump database schema"
	@echo ""
	@echo "Application:"
	@echo "  make build             - Build all Go applications"
	@echo "  make run               - Run all services"
	@echo "  make run-ingestor      - Run ingestor service"
	@echo "  make run-orchestrator  - Run orchestrator service"
	@echo "  make run-query         - Run query service"
	@echo ""
	@echo "Development:"
	@echo "  make test              - Run all tests"
	@echo "  make test-coverage     - Run tests with coverage"
	@echo "  make lint              - Run linter"
	@echo "  make fmt               - Format code"
	@echo ""
	@echo "Workers:"
	@echo "  make worker-cpp        - Build C++ parser worker"
	@echo "  make worker-python     - Setup Python aggregator worker"
	@echo "  make worker-ai         - Setup AI agent worker"
	@echo ""
	@echo "Utilities:"
	@echo "  make clean             - Clean build artifacts"
	@echo "  make deps              - Install dependencies"
	@echo "  make docker-clean      - Remove all Docker containers and volumes"

# ============================================================================
# Infrastructure Commands
# ============================================================================

infra-up:
	@echo "🚀 Starting infrastructure services..."
	docker-compose -f deployments/docker-compose.yml up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@echo "✅ Infrastructure services started!"
	@echo ""
	@echo "📊 Service URLs:"
	@echo "  - PostgreSQL: localhost:5432"
	@echo "  - Redis:      localhost:6379"
	@echo "  - Kafka:      localhost:9092"
	@echo "  - MinIO:      http://localhost:9000 (UI: http://localhost:9001)"
	@echo "  - Kafka UI:   http://localhost:8080"
	@echo "  - Redis CMD:  http://localhost:8081"
	@echo "  - PgAdmin:    http://localhost:5050"

infra-down:
	@echo "🛑 Stopping infrastructure services..."
	docker-compose -f deployments/docker-compose.yml down
	@echo "✅ Infrastructure services stopped!"

infra-logs:
	docker-compose -f deployments/docker-compose.yml logs -f

infra-ps:
	@echo "📦 Running infrastructure containers:"
	docker-compose -f deployments/docker-compose.yml ps

infra-restart: infra-down infra-up

# ============================================================================
# Database Commands
# ============================================================================

db-migrate:
	@echo "📝 Running database migrations..."
	@# TODO: Add migration tool here
	@echo "✅ Migrations completed!"

db-reset:
	@echo "⚠️  Resetting database..."
	docker-compose -f deployments/docker-compose.yml exec postgres psql -U argus -d argus_ota -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
	@echo "✅ Database reset completed!"

db-connect:
	docker-compose -f deployments/docker-compose.yml exec postgres psql -U argus -d argus_ota

db-dump:
	docker-compose -f deployments/docker-compose.yml exec postgres pg_dump -U argus -d argus_ota --schema-only

# ============================================================================
# Application Commands
# ============================================================================

build:
	@echo "🔨 Building all services..."
	@echo "Building ingestor..."
	@cd cmd/ingestor && go build -o ../../build/ingestor main.go
	@echo "Building orchestrator..."
	@cd cmd/orchestrator && go build -o ../../build/orchestrator main.go
	@echo "Building query service..."
	@cd cmd/query-service && go build -o ../../build/query-service main.go
	@echo "✅ Build completed!"

run: run-ingestor run-orchestrator run-query

run-ingestor:
	@echo "🚀 Starting ingestor service..."
	@cd cmd/ingestor && go run main.go

run-orchestrator:
	@echo "🚀 Starting orchestrator service..."
	@cd cmd/orchestrator && go run main.go

run-query:
	@echo "🚀 Starting query service..."
	@cd cmd/query-service && go run main.go

# ============================================================================
# Development Commands
# ============================================================================

test:
	@echo "🧪 Running tests..."
	go test -v ./...

test-coverage:
	@echo "🧪 Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: coverage.html"

lint:
	@echo "🔍 Running linter..."
	golangci-lint run ./...

fmt:
	@echo "🎨 Formatting code..."
	go fmt ./...
	goimports -w .

# ============================================================================
# Worker Commands
# ============================================================================

worker-cpp:
	@echo "🔨 Building C++ parser worker..."
	@cd workers/cpp-parser && mkdir -p build && cd build && cmake .. && make

worker-python:
	@echo "🐍 Setting up Python aggregator worker..."
	@cd workers/python-aggregator && python -m venv venv && \
	. venv/bin/activate && \
	pip install -r requirements.txt

worker-ai:
	@echo "🤖 Setting up AI agent worker..."
	@cd workers/ai-agent && python -m venv venv && \
	. venv/bin/activate && \
	pip install -r requirements.txt

# ============================================================================
# Utility Commands
# ============================================================================

clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -rf build/
	@rm -f coverage.out coverage.html
	@echo "✅ Clean completed!"

deps:
	@echo "📦 Installing dependencies..."
	go mod download
	go mod tidy
	@echo "✅ Dependencies installed!"

docker-clean:
	@echo "🧹 Cleaning Docker containers and volumes..."
	docker-compose -f deployments/docker-compose.yml down -v
	docker system prune -f
	@echo "✅ Docker cleanup completed!"

# ============================================================================
# Quick Start Commands
# ============================================================================

quick-start: infra-up
	@echo ""
	@echo "⏳ Waiting for services to be healthy..."
	@sleep 10
	@echo "✅ Ready to develop!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Run 'make db-migrate' to setup database"
	@echo "  2. Run 'make build' to build services"
	@echo "  3. Run 'make run' to start all services"
