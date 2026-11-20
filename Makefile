.PHONY: help install generate test lint docs docs-serve clean

# Default target
help:
	@echo "Loom 🧵 Development Commands:"
	@echo "  make install     Install development dependencies"
	@echo "  make generate    Run go generate to update LLM profiles"
	@echo "  make test        Run all tests"
	@echo "  make lint        Run golangci-lint (if installed)"
	@echo "  make docs        Build documentation"
	@echo "  make docs-serve  Start local documentation server"
	@echo "  make clean       Remove temporary files and logs"

# Dependencies
install:
	go mod download
	npm --prefix site ci

# Development
generate:
	go generate ./...

test:
	go test -v -race ./...

lint:
	golangci-lint run

# Documentation
docs:
	npm --prefix site run build

docs-serve:
	npm --prefix site run dev

# Cleanup
clean:
	rm -rf site/dist/ site/.astro/
	rm -rf logs/*.json
	rm -rf logs/*.jsonl
