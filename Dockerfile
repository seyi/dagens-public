# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /agent-server ./cmd/agent-server

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

# Create non-root user with numeric UID
RUN adduser -D -u 1000 -g '' appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /agent-server /app/agent-server

# Use non-root user (numeric UID for K8s runAsNonRoot)
USER 1000

EXPOSE 8080

ENTRYPOINT ["/app/agent-server"]
CMD ["-addr", ":8080"]
