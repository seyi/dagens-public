# Dagens Failover Demo
# Multi-stage build for minimal image size

# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies
RUN apk add --no-cache git ca-certificates

# Enable automatic toolchain download for newer Go versions
ENV GOTOOLCHAIN=auto

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Remove vendor directory to use downloaded modules (vendor/modules.txt enforces Go 1.25.2)
RUN rm -rf vendor

# Build the demo binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /failover-demo ./examples/failover_demo

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS calls to OpenRouter
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /failover-demo /app/failover-demo

# Environment variables with defaults
ENV OTEL_EXPORTER_TYPE=otlp
ENV OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4317
# OPENROUTER_API_KEY should be provided at runtime

# Labels for documentation
LABEL org.opencontainers.image.title="Dagens Failover Demo"
LABEL org.opencontainers.image.description="Demonstrates sticky scheduling, automatic failover, and enterprise guardrails"
LABEL org.opencontainers.image.source="https://github.com/seyi/dagens"

# Run the demo
ENTRYPOINT ["/app/failover-demo"]
