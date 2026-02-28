# Makefile for stdio HITL examples

.PHONY: build run demo advanced-demo durable-demo lifecycle-demo test

build:
	go build ./examples/stdio_hitl/main.go
	go build ./examples/stdio_hitl/advanced_demo.go
	go build ./examples/stdio_hitl/durable_demo.go
	go build ./examples/stdio_hitl/full_lifecycle_demo.go

run:
	go run ./examples/stdio_hitl/main.go

demo: run

advanced-demo:
	go run ./examples/stdio_hitl/advanced_demo.go

durable-demo:
	go run ./examples/stdio_hitl/durable_demo.go

lifecycle-demo:
	go run ./examples/stdio_hitl/full_lifecycle_demo.go

test:
	go test ./pkg/graph/... -v

test-stdio:
	go test ./pkg/graph/... -v -run "Stdio"

clean:
	rm -f stdio-hitl-demo
	rm -f advanced-stdio-hitl-demo
	rm -f durable-stdio-hitl-demo
	rm -f lifecycle-stdio-hitl-demo