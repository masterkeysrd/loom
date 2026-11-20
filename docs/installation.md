---
title: Install Loom
description: Add Loom to a Go module and choose what to set up for your workflow.
---

Loom requires Go 1.26.10 or later. In the directory containing your project's `go.mod`, add the graph package.

```sh
go get github.com/masterkeysrd/loom/graph
```

Starting from an empty directory? [Run your first graph](https://masterkeysrd.github.io/loom/guides/quickstart/) walks through creating a module, executing a node, and checking the result.

## Add integrations when you need them

- To call a language model, [choose a provider](./providers.md) and follow its setup instructions. For example, [OpenAI](./guides/providers/openai.md) uses an API key and [Ollama](./guides/providers/ollama.md) connects to a model running locally.
- To resume a workflow after a restart, [configure a SQLite or PostgreSQL checkpointer](./guides/persistence.md).

## Work on Loom itself

From the repository root, `make install` installs Go and documentation dependencies. Use `make docs-serve` to preview the Astro site and `make docs` to build it. Use Node.js 24 and npm to match the documentation build in CI.

Run the Go tests with `make test` and regenerate model profiles with `make generate`.
