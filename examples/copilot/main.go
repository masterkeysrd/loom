package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/masterkeysrd/loom/examples/internal/agentrepl"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomcopilot "github.com/masterkeysrd/loom/providers/copilot"
	"github.com/masterkeysrd/loom/tool"
)

func main() {
	defaultModel := os.Getenv("COPILOT_MODEL")
	if defaultModel == "" {
		defaultModel = "auto"
	}

	modelName := flag.String("model", defaultModel, "Copilot or BYOK model ID (e.g. auto, gpt-5.4, claude-sonnet-4.6, qwen3-coder)")
	cliPath := flag.String("cli-path", os.Getenv("COPILOT_CLI_PATH"), "Optional path to copilot CLI executable (defaults to PATH lookup)")
	baseDir := flag.String("base-dir", os.Getenv("COPILOT_HOME"), "Optional COPILOT_HOME directory (empty uses ~/.copilot)")
	githubToken := flag.String("github-token", os.Getenv("COPILOT_GITHUB_TOKEN"), "Optional GitHub token for Copilot auth")
	endpoint := flag.String("endpoint", os.Getenv("COPILOT_PROVIDER_BASE_URL"), "Optional BYOK base URL (e.g. http://localhost:11434/v1 for Ollama)")
	providerType := flag.String("provider-type", "openai", "BYOK provider type when --endpoint is set: openai, azure, anthropic, google")
	apiKey := flag.String("api-key", os.Getenv("COPILOT_PROVIDER_API_KEY"), "Optional BYOK API key when --endpoint is set")
	prompt := flag.String("prompt", "", "Optional single prompt (if omitted, starts interactive REPL)")
	syncOnWrite := flag.Bool("sync-on-write", false, "Enable strict SyncOnWrite (*llm.OutOfSyncError) reconciliation")
	allowAll := flag.Bool("allow-all", false, "Auto-approve Copilot runtime permission requests when native tools are enabled")
	debug := flag.Bool("debug", false, "Print structured Copilot SDK session trace lines to stderr")
	flag.Parse()

	var opts []loomcopilot.Option
	if *cliPath != "" {
		opts = append(opts, loomcopilot.WithCLIPath(*cliPath))
	}
	if *baseDir != "" {
		opts = append(opts, loomcopilot.WithBaseDirectory(*baseDir))
	}
	if *githubToken != "" {
		opts = append(opts, loomcopilot.WithGitHubToken(*githubToken))
	}
	if *endpoint != "" {
		opts = append(opts, loomcopilot.WithBYOKProvider(&copilot.ProviderConfig{
			Type:    *providerType,
			BaseURL: *endpoint,
			APIKey:  *apiKey,
		}))
	}
	if *syncOnWrite {
		opts = append(opts, loomcopilot.WithSyncOnWrite(true))
	}
	if *allowAll {
		opts = append(opts, loomcopilot.WithAllowAllPermissions(true))
	}
	if *debug {
		opts = append(opts, loomcopilot.WithTraceWriter(os.Stderr))
	}
	opts = append(opts, loomcopilot.WithOnIdleMessage(func(msg message.Message) {
		fmt.Printf("\n[Idle (%s)] %s\n", msg.Role(), msg.GetContent().Text())
	}))

	provider, err := loomcopilot.NewDefaultProvider(opts...)
	if err != nil {
		log.Fatalf("failed to create copilot provider: %v", err)
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
		Title:        "Loom Copilot SDK REPL (commands: /compact [focus], exit)",
		GraphName:    "CopilotAgent",
		SystemPrompt: "You are a helpful coding assistant. Use shell_command only when needed.",
		Prompt:       *prompt,
		Stream:       true,
		Tools:        []*tool.Tool{shellTool},
		OnCommand: func(line string, _ *message.MessageList) (bool, []llm.CallOption) {
			if line == "/compact" || strings.HasPrefix(line, "/compact ") {
				instructions := strings.TrimSpace(strings.TrimPrefix(line, "/compact"))
				return true, []llm.CallOption{
					llm.WithExtensionOption(loomcopilot.Session{
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
