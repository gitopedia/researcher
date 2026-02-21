.PHONY: all build test lint fmt clean launch

BINARY_NAME=researcher
BIN_DIR=bin
REPO_PATH=../gitopedia

all: fmt lint test build

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) main.go

test:
	go test -v ./...

fmt:
	go fmt ./...

lint:
	go vet ./...
	# Add staticcheck if installed (go install honnef.co/go/tools/cmd/staticcheck@latest)
	# staticcheck ./...

clean:
	go clean
	rm -f $(BIN_DIR)/$(BINARY_NAME)
	rm -f $(BINARY_NAME)

# Launch both API server and dashboard
# Usage: make launch [REPO_PATH=../gitopedia]
launch: build
	@echo "🚀 Launching Gitopedia Researcher..."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "📡 Starting API Server (port 3001)..."
	@$(BIN_DIR)/$(BINARY_NAME) --server --repo-path $(REPO_PATH) & \
	API_PID=$$!; \
	echo "✅ API Server started (PID: $$API_PID)"; \
	echo ""; \
	echo "🎨 Starting Dashboard (port 3000)..."; \
	cd dashboard && npm run dev & \
	DASH_PID=$$!; \
	echo "✅ Dashboard started (PID: $$DASH_PID)"; \
	echo ""; \
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; \
	echo "✨ Both services are running!"; \
	echo "📊 Dashboard: http://localhost:3000"; \
	echo "🔌 API Server: http://127.0.0.1:3001"; \
	echo ""; \
	echo "💡 Press Ctrl+C to stop both services"; \
	trap 'kill $$API_PID $$DASH_PID 2>/dev/null; echo "✅ All services stopped. Goodbye!"; exit' INT TERM; \
	wait












