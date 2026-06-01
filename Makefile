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
	pip install -r docs/requirements.txt

# Development
generate:
	go generate ./...

test:
	go test -v -race ./...

lint:
	golangci-lint run

# Documentation
docs:
	python3 -m mkdocs build

docs-serve:
	python3 -m mkdocs serve

# Cleanup
clean:
	rm -rf site/
	rm -rf logs/*.json
	rm -rf logs/*.jsonl
