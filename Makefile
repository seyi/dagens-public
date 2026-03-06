.PHONY: all build clean test install run-examples proto help

# Variables
GO_PACKAGE := github.com/apache/spark/spark-ai-agents
PYTHON_DIR := python
BINDINGS_DIR := $(PYTHON_DIR)/bindings
LIB_NAME := libsparkagents.so
PROTO_DIR := pkg/rpc

# Default target
all: build

# Help target
help:
	@echo "Spark AI Agents - Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  build          Build the Go shared library for Python bindings"
	@echo "  clean          Remove build artifacts"
	@echo "  test           Run tests for both Go and Python"
	@echo "  test-go        Run Go tests only"
	@echo "  test-python    Run Python tests only"
	@echo "  install        Install Python package locally"
	@echo "  run-examples   Run example scripts"
	@echo "  proto          Generate protobuf code"
	@echo "  fmt            Format Go code"
	@echo "  lint           Run linters"
	@echo "  help           Show this help message"

# Build shared library for Python bindings
build:
	@echo "Building Go shared library..."
	@mkdir -p $(BINDINGS_DIR)
	go build -buildmode=c-shared -o $(BINDINGS_DIR)/$(LIB_NAME) $(BINDINGS_DIR)/spark_agents.go
	@echo "Build complete: $(BINDINGS_DIR)/$(LIB_NAME)"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINDINGS_DIR)/$(LIB_NAME)
	rm -f $(BINDINGS_DIR)/*.h
	rm -rf $(PYTHON_DIR)/build
	rm -rf $(PYTHON_DIR)/dist
	rm -rf $(PYTHON_DIR)/*.egg-info
	find . -type d -name __pycache__ -exec rm -rf {} +
	find . -type f -name "*.pyc" -delete
	@echo "Clean complete"

# Run all tests
test: test-go test-python

# Run Go tests
test-go:
	@echo "Running Go tests..."
	go test -v ./...

# Run Python tests
test-python:
	@echo "Running Python tests..."
	cd $(PYTHON_DIR) && python -m pytest tests/ -v

# Install Python package
install: build
	@echo "Installing Python package..."
	cd $(PYTHON_DIR) && pip install -e .
	@echo "Installation complete"

# Run example scripts
run-examples: install
	@echo "Running simple example..."
	python examples/simple_agent.py
	@echo ""
	@echo "Running hierarchical example..."
	python examples/hierarchical_agents.py

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go-grpc_out=. $(PROTO_DIR)/protocol.proto
	@echo "Protobuf generation complete"

# Format Go code
fmt:
	@echo "Formatting Go code..."
	go fmt ./...
	@echo "Formatting complete"

# Run linters
lint:
	@echo "Running linters..."
	golangci-lint run ./...
	@echo "Linting complete"

# Development setup
dev-setup:
	@echo "Setting up development environment..."
	go mod download
	pip install -r $(PYTHON_DIR)/requirements-dev.txt
	@echo "Development setup complete"
