.PHONY: all build test lint fmt clean

BINARY_NAME=researcher
BIN_DIR=bin

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












