package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	quota := flag.Bool("quota", false, "Inspect account usage limits via /usage and exit")
	syncOnWrite := flag.Bool("sync-on-write", false, "Enable strict SyncOnWrite (*llm.OutOfSyncError) reconciliation")
	debug := flag.Bool("debug", false, "Print raw Claude CLI stdin/stdout/stderr protocol frames to stderr")
	flag.Parse()

	rt := loomclaudecode.SpawnedStdio{
		ConfigDir: *configDir,
	}
	if *endpoint != "" {
		tok := *authToken
		if tok == "" {
			tok = "loom-custom-endpoint"
		}
		rt.Env = append(os.Environ(),
			"ANTHROPIC_BASE_URL="+*endpoint,
			"ANTHROPIC_AUTH_TOKEN="+tok,
			"ANTHROPIC_API_KEY=",
			// Keep Claude Code background Haiku/telemetry calls from hitting missing models on custom endpoints.
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL="+*modelName,
			"ANTHROPIC_SMALL_FAST_MODEL="+*modelName,
		)
	}
	obs := loomclaudecode.ObservabilityOptions{
		OnIdleMessage: func(msg message.Message) {
			fmt.Printf("\n[Idle (%s)] %s\n", msg.Role(), msg.GetContent().Text())
		},
	}
	if *debug {
		obs.TraceWriter = os.Stderr
	}

	provider, err := loomclaudecode.NewProvider(&loomclaudecode.Config{
		Runtime: rt,
		Session: loomclaudecode.SessionOptions{
			SyncOnWrite: *syncOnWrite,
		},
		Observability: obs,
	})
	if err != nil {
		log.Fatalf("failed to create claudecode provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	if *quota {
		if err := printQuotas(ctx, provider); err != nil {
			log.Printf("failed to inspect quotas: %v", err)
		}
		return
	}

	model, err := llm.NewModel(provider, *modelName, nil)
	if err != nil {
		log.Fatalf("failed to create model: %v", err)
	}

	shellTool, err := agentrepl.NewShellTool()
	if err != nil {
		log.Fatalf("failed to create shell tool: %v", err)
	}

	err = agentrepl.Run(ctx, model, agentrepl.Options{
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

func printQuotas(ctx context.Context, provider any) error {
	fmt.Println("\n--- Claude Code Usage & Rate Limits (/usage) ---")
	qp, ok := provider.(llm.QuotaProvider)
	if !ok {
		fmt.Println("Provider does not implement llm.QuotaProvider")
		return nil
	}
	snap, err := qp.GetQuotaSnapshot(ctx)
	if err != nil {
		return err
	}
	if len(snap.Buckets) == 0 {
		fmt.Println("No rate limits reported (requires a claude.ai subscription login; API keys return session totals only).")
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(snap.Buckets)) {
		b := snap.Buckets[id]
		winStr := ""
		switch {
		case b.Window <= 0:
		case b.Window%(24*time.Hour) == 0:
			winStr = fmt.Sprintf("(%dd)", int(b.Window/(24*time.Hour)))
		case b.Window%time.Hour == 0:
			winStr = fmt.Sprintf("(%dh)", int(b.Window/time.Hour))
		default:
			winStr = fmt.Sprintf("(%s)", b.Window)
		}
		usageStr := ""
		if b.Limit > 0 {
			usageStr = fmt.Sprintf(" [%.0f/%.0f %s]", b.Used, b.Limit, b.Unit)
		}
		resetStr := ""
		if b.ResetAt != nil {
			resetStr = fmt.Sprintf(" (resets at %s)", b.ResetAt.Format(time.RFC3339))
		}
		fmt.Printf("• %-20s %-6s | Remaining: %5.1f%%%s%s\n", id, winStr, b.Percentage(), usageStr, resetStr)
	}
	return nil
}
