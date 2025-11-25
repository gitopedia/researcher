FROM golang:1.23-alpine AS builder

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
COPY env.example .

# Environment variables should be provided at runtime
# ENV GITHUB_TOKEN=...

CMD ["./researcher"]

