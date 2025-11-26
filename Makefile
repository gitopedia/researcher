.PHONY: all build test lint fmt clean

BINARY_NAME=researcher

all: fmt lint test build

build:
	go build -o $(BINARY_NAME) main.go

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
	rm -f $(BINARY_NAME)






