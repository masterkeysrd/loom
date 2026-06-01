# Ollama Provider 🔌

The Ollama provider allows you to run open-source models (like Llama 3, Mistral, Qwen) locally on your machine.

## 1. Setup

Ollama must be installed and running on your system. You can download it from [ollama.com](https://ollama.com).

By default, Loom connects to the standard Ollama API at `http://localhost:11434`.

## 2. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/ollama"
)

provider, err := loomollama.NewDefaultProvider()
if err != nil {
    // handle error
}

// Make sure you have pulled the model locally: ollama pull llama3.1
model := llm.NewModel(provider, "llama3.1")
```

## 3. Local Model Catalog

Unlike cloud providers, Ollama's available models depend on what you have installed locally. You can register your local models with Loom to ensure context window metadata is available:

```go
provider.OverrideProfile("my-local-llama", llm.ModelProfile{
    ID: "my-local-llama",
    DisplayName: "Local Llama 3",
    Limits: llm.ModelLimits{
        Context: 8192,
    },
})
```
