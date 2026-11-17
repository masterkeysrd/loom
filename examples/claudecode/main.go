package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/masterkeysrd/loom/examples/internal/agentrepl"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomclaudecode "github.com/masterkeysrd/loom/providers/claudecode"
	"github.com/masterkeysrd/loom/tool"
)

func main() {
	defaultModel := os.Getenv("CLAUDE_MODEL")
	if defaultModel == "" {
		defaultModel = "sonnet"
	}
	defaultConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if defaultConfigDir == "" && os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			defaultConfigDir = filepath.Join(home, ".claude")
		}
	}

	modelName := flag.String("model", defaultModel, "Model ID or Claude Code alias (e.g. sonnet, opus, qwen3-coder)")
	endpoint := flag.String("endpoint", os.Getenv("ANTHROPIC_BASE_URL"), "Optional Anthropic-compatible base URL (e.g. http://localhost:11434 for Ollama)")
	authToken := flag.String("auth-token", os.Getenv("ANTHROPIC_AUTH_TOKEN"), "Optional ANTHROPIC_AUTH_TOKEN when using --endpoint")
	configDir := flag.String("config-dir", defaultConfigDir, "Optional CLAUDE_CONFIG_DIR (empty uses an isolated temp directory)")
	prompt := flag.String("prompt", "", "Optional single prompt (if omitted, starts interactive REPL)")
	syncOnWrite := flag.Bool("sync-on-write", false, "Enable strict SyncOnWrite (*llm.OutOfSyncError) reconciliation")
	debug := flag.Bool("debug", false, "Print raw Claude CLI stdin/stdout/stderr protocol frames to stderr")
	flag.Parse()

	var opts []loomclaudecode.Option
	if *configDir != "" {
		opts = append(opts, loomclaudecode.WithConfigDir(*configDir))
	}
	if *endpoint != "" {
		tok := *authToken
		if tok == "" {
			tok = "loom-custom-endpoint"
		}
		opts = append(opts, loomclaudecode.WithEnv(
			"ANTHROPIC_BASE_URL="+*endpoint,
			"ANTHROPIC_AUTH_TOKEN="+tok,
			"ANTHROPIC_API_KEY=",
			// Keep Claude Code background Haiku/telemetry calls from hitting missing models on custom endpoints.
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL="+*modelName,
			"ANTHROPIC_SMALL_FAST_MODEL="+*modelName,
		))
	}
	if *syncOnWrite {
		opts = append(opts, loomclaudecode.WithSyncOnWrite(true))
	}
	if *debug {
		opts = append(opts, loomclaudecode.WithTraceWriter(os.Stderr))
	}
	opts = append(opts, loomclaudecode.WithOnIdleMessage(func(msg message.Message) {
		fmt.Printf("\n[Idle (%s)] %s\n", msg.Role(), msg.GetContent().Text())
	}))

	provider, err := loomclaudecode.NewDefaultProvider(opts...)
	if err != nil {
		log.Fatalf("failed to create claudecode provider: %v", err)
	}
	defer provider.Close()

	model, err := llm.NewModel(provider, *modelName, nil)
	if err != nil {
		log.Fatalf("failed to create model: %v", err)
	}

	shellTool, err := agentrepl.NewShellTool()
	if err != nil {
		log.Fatalf("failed to create shell tool: %v", err)
	}

	err = agentrepl.Run(context.Background(), model, agentrepl.Options{
		Title:        "Loom Claude Code REPL (commands: /compact [focus], exit)",
		GraphName:    "ClaudeCodeAgent",
		SystemPrompt: "You are a helpful coding assistant. Use shell_command only when needed.",
		Prompt:       *prompt,
		Stream:       true,
		Tools:        []*tool.Tool{shellTool},
		OnCommand: func(line string, _ *message.MessageList) (bool, []llm.CallOption) {
			if line == "/compact" || strings.HasPrefix(line, "/compact ") {
				instructions := strings.TrimSpace(strings.TrimPrefix(line, "/compact"))
				return true, []llm.CallOption{
					llm.WithExtensionOption(loomclaudecode.Session{
						Compact:             true,
						CompactInstructions: instructions,
					}),
				}
			}
			return false, nil
		},
	})
	if err != nil {
		log.Fatalf("agent failed: %v", err)
	}
}
