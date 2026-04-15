.PHONY: help dev deps build clean test lint hooks docker-up docker-down server worker web logs review

# Default target
help:
	@echo "Wachd Development Commands"
	@echo ""
	@echo "Setup:"
	@echo "  make deps        - Install Go + npm dependencies"
	@echo "  make docker-up   - Start Postgres + Redis + Ollama"
	@echo "  make docker-down - Stop all Docker services"
	@echo ""
	@echo "Development:"
	@echo "  make dev         - Start server + worker + web in parallel"
	@echo "  make server      - Run server only"
	@echo "  make worker      - Run worker only"
	@echo "  make web         - Run Next.js frontend only"
	@echo ""
	@echo "Build:"
	@echo "  make build       - Build binaries"
	@echo "  make clean       - Clean build artifacts"
	@echo ""
	@echo "Testing & Review:"
	@echo "  make test        - Run unit tests (no external deps)"
	@echo "  make test-e2e    - Run end-to-end tests (requires Postgres + Redis)"
	@echo "  make test-integration - Run all integration tests"
	@echo "  make lint        - Run golangci-lint (same as CI)"
	@echo "  make review      - Run code quality + security review"
	@echo "  make test-webhook - Send test webhook"
	@echo "  make hooks       - Install git pre-push hook"
	@echo ""
	@echo "Logs:"
	@echo "  make logs        - Follow Docker logs"

# Install dependencies
deps:
	@echo "📦 Installing Go dependencies..."
	go mod download
	go mod tidy
	@echo "📦 Installing npm dependencies..."
	cd web && npm install

# Start Docker services
docker-up:
	@echo "🐳 Starting Postgres + Redis..."
	docker-compose up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@echo "✓ Services ready!"

# Stop Docker services
docker-down:
	@echo "🛑 Stopping services..."
	docker-compose down

# Build binaries
build:
	@echo "🔨 Building binaries..."
	go build -o bin/server ./cmd/server
	go build -o bin/worker ./cmd/worker
	@echo "✓ Binaries built in ./bin/"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -rf bin/
	rm -f cmd/server/server cmd/worker/worker

# Run server
server:
	@echo "🚀 Starting server..."
	@if [ ! -f .env ]; then cp .env.example .env; fi
	@set -a; source .env; set +a; go run ./cmd/server

# Run worker
worker:
	@echo "🔄 Starting worker..."
	@if [ ! -f .env ]; then cp .env.example .env; fi
	@set -a; source .env; set +a; go run ./cmd/worker

# Run web frontend
web:
	@echo "🌐 Starting Next.js frontend..."
	cd web && PORT=3000 npm run dev

# Run server + worker + web
dev: docker-up
	@echo "🚀 Starting development environment..."
	@if [ ! -f .env ]; then cp .env.example .env; fi
	@echo ""
	@echo "Starting server, worker, and web frontend..."
	@echo "  - Server: http://localhost:8080"
	@echo "  - Frontend: http://localhost:3000"
	@echo ""
	@echo "Press Ctrl+C to stop all processes"
	@echo ""
	@set -a; source .env; set +a; \
	trap 'kill 0' SIGINT; \
	go run ./cmd/server & \
	go run ./cmd/worker & \
	(cd web && PORT=3000 npm run dev) & \
	wait

# Run unit tests (no external dependencies)
test:
	@echo "🧪 Running unit tests..."
	go test -v ./...

# Run e2e integration tests (requires running Postgres + Redis)
test-e2e:
	@echo "🧪 Running end-to-end tests (requires Postgres + Redis)..."
	@set -o pipefail; go test -json -tags integration -count=1 -run TestE2E ./cmd/server/ 2>&1 | python3 scripts/test-table.py

# Run integration tests (requires running Postgres + Redis)
test-integration:
	@echo "🧪 Running integration tests (requires Postgres + Redis)..."
	go test -tags integration -v -count=1 ./...

# Run full test suite — unit + integration. Use before every release.
test-release:
	@echo "🧪 Running full test suite for release..."
	go test -v ./...
	go test -tags integration -v -count=1 ./...
	@echo "✓ All tests passed — safe to release"

# Send test webhook
test-webhook:
	@echo "📤 Sending test webhook..."
	@curl -X POST http://localhost:8080/api/v1/webhook/00000000-0000-0000-0000-000000000001/demo_secret_123 \
		-H "Content-Type: application/json" \
		-d '{ \
			"title": "High CPU Usage - web-server", \
			"ruleName": "cpu_threshold", \
			"state": "alerting", \
			"message": "CPU usage is at 95% for the last 5 minutes", \
			"tags": { \
				"service": "web-server", \
				"env": "production", \
				"severity": "high" \
			}, \
			"evalMatches": [ \
				{"metric": "cpu_usage", "value": 0.95} \
			] \
		}' \
		&& echo "" \
		&& echo "✓ Webhook sent!"

# Follow Docker logs
logs:
	docker-compose logs -f

# Check services health
status:
	@echo "🔍 Checking service health..."
	@curl -s http://localhost:8080/api/v1/health | jq . || echo "Server not running"
	@docker-compose ps

# Run linter (mirrors CI exactly)
lint:
	@echo "🔍 Running golangci-lint..."
	@if ! command -v golangci-lint > /dev/null 2>&1; then \
		echo "Installing golangci-lint v2.11.4..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4; \
	fi
	golangci-lint run ./...

# Install git hooks
hooks:
	@echo "🔗 Installing git hooks..."
	@git config core.hooksPath .githooks
	@echo "✓ Pre-push hook active. Run 'make lint' to lint manually."

# Run code review and security checks
review:
	@echo "🔍 Running code quality and security review..."
	@chmod +x scripts/review.sh
	@./scripts/review.sh
