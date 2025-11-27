FROM golang:alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o researcher main.go

FROM alpine:latest

# Install Chromium for headless usage
RUN apk add --no-cache chromium

# Set up app
WORKDIR /app
COPY --from=builder /app/researcher .
COPY --from=builder /app/config ./config

# Environment variables are provided at runtime via docker-compose.yml
# The .env file is loaded by docker-compose using env_file directive
# The Go application loads config/base.env first, then .env for overrides

CMD ["./researcher"]

