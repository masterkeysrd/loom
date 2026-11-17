package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	loomauth "github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/examples/internal/agentrepl"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	loomantigravity "github.com/masterkeysrd/loom/providers/antigravity"
	"golang.org/x/oauth2"
)

func main() {
	var (
		modelFlag      string
		promptFlag     string
		effortFlag     string
		loginFlag      bool
		listModelsFlag bool
		prodFlag       bool
		rawFlag        bool
		quotaFlag      bool
		countFlag      string
		tokenFileFlag  string
	)

	flag.StringVar(&modelFlag, "model", "gemini-3.8-flash", "Model to use for generation")
	flag.StringVar(&promptFlag, "prompt", "", "Prompt to send (if omitted, interactive mode starts)")
	flag.StringVar(&effortFlag, "effort", "", "Thinking effort level: low, medium, or high")
	flag.StringVar(&countFlag, "count-tokens", "", "Count tokens for the given text using the Antigravity TokenCounter")
	flag.StringVar(&tokenFileFlag, "token-file", "", "Path to token cache file (defaults to ~/.config/loom/antigravity_token.json)")
	flag.BoolVar(&loginFlag, "login", false, "Launch interactive browser login to obtain OAuth credentials")
	flag.BoolVar(&listModelsFlag, "list-models", false, "List available models and exit")
	flag.BoolVar(&quotaFlag, "quota", false, "Inspect model quotas and rate-limit replenishment windows")
	flag.BoolVar(&prodFlag, "prod", false, "Use the production endpoint (cloudcode-pa.googleapis.com) instead of daily canary")
	flag.BoolVar(&rawFlag, "raw", false, "Expose raw backend model endpoints instead of compact canonical profiles")
	flag.Parse()

	ctx := context.Background()

	if tokenFileFlag == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			tokenFileFlag = filepath.Join(home, ".config", "loom", "antigravity_token.json")
		}
	}

	oauthCfg := loomantigravity.NewOAuthConfig(loomantigravity.OAuthOptions{})

	// Handle explicit login flag
	if loginFlag {
		fmt.Println("Initiating OAuth login flow...")
		tok, err := loomauth.LoginLoopback(ctx, oauthCfg, loomauth.LoopbackOptions{
			OnAuthURL: func(url string) {
				fmt.Printf("\nOpening browser for Antigravity Google OAuth login...\nURL: %s\n\n", url)
				_ = loomauth.OpenBrowser(url)
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
		if tokenFileFlag != "" {
			if err := loomauth.SaveTokenFile(tokenFileFlag, tok); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to save token to %q: %v\n", tokenFileFlag, err)
				os.Exit(1)
			}
		}
		fmt.Println("Login successful! Token saved to disk.")
		if promptFlag == "" && !listModelsFlag {
			return
		}
	}

	persistentSource := loomauth.NewPersistentTokenSource(ctx, oauthCfg, loomauth.PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			if tokenFileFlag == "" {
				return nil, os.ErrNotExist
			}
			return loomauth.LoadTokenFile(tokenFileFlag)
		},
		Save: func(tok *oauth2.Token) error {
			if tokenFileFlag == "" {
				return nil
			}
			return loomauth.SaveTokenFile(tokenFileFlag, tok)
		},
		Login: func(ctx context.Context) (*oauth2.Token, error) {
			return loomauth.LoginLoopback(ctx, oauthCfg, loomauth.LoopbackOptions{
				OnAuthURL: func(url string) {
					fmt.Printf("\nOpening browser for Antigravity Google OAuth login...\nURL: %s\n\n", url)
					_ = loomauth.OpenBrowser(url)
				},
			})
		},
	})

	// Determine token source: check env var first, then persistent token source
	var tokenSource = loomauth.EnvTokenSource("ANTIGRAVITY_TOKEN", "GOOGLE_ACCESS_TOKEN")
	if _, err := tokenSource.Token(); err != nil {
		tokenSource = persistentSource
		if _, err := tokenSource.Token(); err != nil {
			fmt.Println("No active credentials found.")
			fmt.Println("To authenticate, run:")
			fmt.Println("  go run ./examples/antigravity --login")
			fmt.Println("Or set an environment variable:")
			fmt.Println("  export ANTIGRAVITY_TOKEN=\"<your-token>\"")
			os.Exit(1)
		}
	}

	// 2. Configure Antigravity Provider
	baseURL := loomantigravity.DailyBaseURL
	if prodFlag {
		baseURL = loomantigravity.DefaultBaseURL
		fmt.Printf("[Using Production Endpoint: %s]\n", baseURL)
	} else {
		fmt.Printf("[Using Daily Canary Endpoint: %s]\n", baseURL)
	}

	provider, err := loomantigravity.NewProvider(&loomantigravity.Config{
		TokenSource: tokenSource,
		BaseURL:     baseURL,
		RawProfiles: rawFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing provider: %v\n", err)
		os.Exit(1)
	}

	// Handle --count-tokens flag
	if countFlag != "" {
		counter, ok := any(provider).(llm.TokenCounter)
		if !ok {
			fmt.Fprintf(os.Stderr, "Provider does not implement llm.TokenCounter\n")
			os.Exit(1)
		}
		msgs := message.MessageList{
			message.NewUserText(countFlag),
		}
		count, err := counter.CountTokens(ctx, msgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error counting tokens: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Token count for %q: %d tokens\n", countFlag, count)
		return
	}

	// 3. Handle --quota flag
	if quotaFlag {
		fmt.Println("\n--- Antigravity Model Quotas & Rate Limits ---")
		qp, ok := any(provider).(llm.QuotaProvider)
		if !ok {
			fmt.Println("Provider does not implement llm.QuotaProvider")
			return
		}
		snap, err := qp.GetQuotaSnapshot(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching quotas: %v\n", err)
			os.Exit(1)
		}
		if len(snap.Models) == 0 && len(snap.Buckets) == 0 {
			fmt.Println("No quota information reported by server.")
			return
		}
		if len(snap.Buckets) > 0 {
			fmt.Println("Pools & Windows:")
			for _, id := range slices.Sorted(maps.Keys(snap.Buckets)) {
				b := snap.Buckets[id]
				winStr := formatWindow(b.Window)
				resetStr := ""
				if b.ResetAt != nil {
					resetStr = fmt.Sprintf(" (resets at %s)", b.ResetAt.Format(time.RFC3339))
				}
				fmt.Printf("  • %-28s %-6s | Remaining: %5.1f%%%s\n", id, winStr, b.Percentage(), resetStr)
			}
		}
		if len(snap.Models) > 0 {
			fmt.Println("\nModels:")
			for _, model := range slices.Sorted(maps.Keys(snap.Models)) {
				buckets, ok := snap.ForModel(model)
				if !ok {
					continue
				}
				bottleneck, ok := buckets.Bottleneck()
				if !ok {
					continue
				}
				var parts []string
				for _, b := range buckets {
					parts = append(parts, fmt.Sprintf("%s: %.1f%%", b.ID, b.Percentage()))
				}
				resetStr := ""
				if bottleneck.ResetAt != nil {
					resetStr = fmt.Sprintf(" (bottleneck resets at %s)", bottleneck.ResetAt.Format(time.RFC3339))
				}
				fmt.Printf("  • %-32s | Remaining: %5.1f%% [%s]%s\n", model, bottleneck.Percentage(), strings.Join(parts, ", "), resetStr)
			}
		}
		return
	}

	// 4. Handle --list-models flag
	if listModelsFlag {
		if rawFlag {
			fmt.Println("\n--- Raw Antigravity Backend Model Endpoints (--raw) ---")
		} else {
			fmt.Println("\n--- Antigravity Models (Compact View - use --raw for all endpoints) ---")
		}

		profiles := provider.ListProfiles()
		qp, _ := any(provider).(llm.QuotaProvider)

		for _, p := range profiles {
			var caps []string
			if p.Capabilities.Reasoning {
				efforts := ""
				for _, opt := range p.Capabilities.ReasoningOptions {
					if opt.Type == "effort" && len(opt.Values) > 0 {
						efforts = fmt.Sprintf(" (efforts: %s)", strings.Join(opt.Values, "/"))
					}
				}
				caps = append(caps, "Thinking"+efforts)
			}
			for _, in := range p.Modalities.Inputs {
				switch in {
				case profile.ModalityImage:
					caps = append(caps, "Vision")
				case profile.ModalityVideo:
					caps = append(caps, "Video")
				case profile.ModalityPDF:
					caps = append(caps, "PDF")
				case profile.ModalityAudio:
					caps = append(caps, "Audio")
				}
			}

			quotaStr := ""
			if qp != nil {
				if buckets, ok, _ := qp.GetQuota(ctx, p.ID); ok {
					if b, ok := buckets.Bottleneck(); ok {
						quotaStr = fmt.Sprintf(" | Quota: %4.1f%%", b.Percentage())
					}
				}
			}

			fmt.Printf("• %-26s | %-32s (Ctx: %4dk, Out: %2dk)%s [%s]\n",
				p.ID, p.Name, p.Limits.Context/1024, p.Limits.Output/1024, quotaStr, strings.Join(caps, ", "))
		}
		fmt.Printf("\nTotal: %d models available.\n", len(profiles))
		return
	}

	// 5. Run interactive REPL or single-prompt mode via shared agentrepl
	model, err := llm.NewModel(provider, modelFlag, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating model: %v\n", err)
		os.Exit(1)
	}
	model = model.WithTemperature(0.7)
	if effortFlag != "" {
		model = model.WithThinkingEffort(effortFlag)
	} else {
		model = model.WithThinking(2048)
	}

	if err := agentrepl.Run(ctx, model, agentrepl.Options{
		Title:     fmt.Sprintf("Antigravity Provider (%s)", modelFlag),
		GraphName: "AntigravityAgent",
		Prompt:    promptFlag,
		Stream:    true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}

func formatWindow(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("(%dd)", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("(%dh)", int(d/time.Hour))
	default:
		return fmt.Sprintf("(%s)", d)
	}
}
