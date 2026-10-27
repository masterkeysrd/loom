# Agent Guidelines 🤖

This document provides instructions and guidelines for AI coding agents working on the `loom` repository.

---

## General Development Standards

* **Testing**: Always run relevant package tests before committing changes (e.g., `go test ./...` or `go test ./providers/openai/...`).
* **Conventional Commits**: Format commit messages according to Conventional Commits:
  - `chore(providers): ...`
  - `feat(graph): ...`
  - `refactor(...): ...`
  - Always include a detailed multi-line description listing specific additions, removals, or capability changes.
* **Documentation**: When adding new features or extensions, update the corresponding markdown guide under `docs/guides/` and keep API docs in sync.

## Provider-Specific Guidelines

### OpenAI Provider Rules

#### New Model Profile & Responses API Checklist
Whenever you add or update models in [`providers/openai/profiles.gen.go`](file:///Users/masterkeysrd/Projects/loom/providers/openai/profiles.gen.go) or configure model behaviors in [`providers/openai/`](file:///Users/masterkeysrd/Projects/loom/providers/openai/):

> [!IMPORTANT]
> **Before committing any new model or profile change in `providers/openai/`**, you **MUST** check whether the model should be added to the preferred Responses API routing.

##### Criteria for Responses API Preferred Routing:
Check whether the model:
1. **Requires or prefers the Responses API (`/v1/responses`)**:
   - Belongs to the GPT-5/GPT-6 Pro family (e.g., `gpt-5-pro`, `gpt-5.2-pro`, `gpt-5.4-pro`, `gpt-5.5-pro`, `gpt-5.6-sol`).
   - Is a coding/agentic model (contains `codex`, e.g., `gpt-5-codex`, `gpt-5.1-codex-max`, `gpt-5.3-codex`).
   - Is a deep research model (contains `deep-research`, e.g., `o3-deep-research`, `o4-mini-deep-research`).
   - Is a computer use model (starts with `computer-use`, e.g., `computer-use-preview`).
   - Is a specialized domain model (e.g., `gpt-5.6-cyber`, `gpt-rosalind-research`).
   - Relies on server-side conversation state, built-in tools (`web_search`, `file_search`), or encrypted reasoning tokens (`encrypted_content`).
2. **Action Required**:
   - If the model matches any of the above, ensure [`modelPrefersResponsesAPI`](file:///Users/masterkeysrd/Projects/loom/providers/openai/provider.go) in `providers/openai/provider.go` routes it to `/v1/responses`.
   - Add/update unit test cases in [`providers/openai/responses_test.go`](file:///Users/masterkeysrd/Projects/loom/providers/openai/responses_test.go) (`TestShouldUseResponsesAPI`).
   - Verify that `go test -v ./providers/openai/ -run TestShouldUseResponsesAPI` passes.

---
