# Installation 📦

Loom is a Go library. To include it in your project, use `go get`.

## Requirements

- **Go**: 1.23 or higher is recommended (Loom uses generics and modern Go features).
- **Environment**: Linux, macOS, or Windows.

## Basic Installation

```bash
go get github.com/masterkeysrd/loom
```

## LLM Providers

Depending on which LLM providers you intend to use, you may need to set specific environment variables for authentication.

| Provider | Requirement |
|---|---|
| **OpenAI** | `OPENAI_API_KEY` |
| **Anthropic** | `ANTHROPIC_API_KEY` |
| **Google Gemini** | `GOOGLE_API_KEY` |
| **Ollama** | Local Ollama instance running |

## Persistence Backends

Loom supports SQLite and PostgreSQL for checkpointing. These are optional but required if you want state persistence.

### SQLite
Requires the `github.com/mattn/go-sqlite3` driver.

```bash
go get github.com/mattn/go-sqlite3
```

### PostgreSQL
Requires a standard PostgreSQL connection string.

## Development Setup

If you plan to contribute to Loom or run the documentation locally, you can use the provided `Makefile` to simplify the setup.

```bash
# Install all development dependencies (Go modules and MkDocs)
make install

# Start the documentation server
make docs-serve
```

Available commands:
- `make generate`: Run code generation for LLM profiles.
- `make test`: Run all tests with race detection.
- `make clean`: Cleanup logs and temporary files.

---
Once installed
, proceed to the [Quick Start Guide](./guides/quickstart.md) to build your first agent!
