package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	loomantigravity "github.com/masterkeysrd/loom/llm/antigravity"
	"github.com/masterkeysrd/loom/message"
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
	)

	flag.StringVar(&modelFlag, "model", "gemini-3.8-flash", "Model to use for generation")
	flag.StringVar(&promptFlag, "prompt", "", "Prompt to send (if omitted, interactive mode starts)")
	flag.StringVar(&effortFlag, "effort", "", "Thinking effort level: low, medium, or high")
	flag.StringVar(&countFlag, "count-tokens", "", "Count tokens for the given text using the Antigravity TokenCounter")
	flag.BoolVar(&loginFlag, "login", false, "Launch interactive browser login to obtain OAuth credentials")
	flag.BoolVar(&listModelsFlag, "list-models", false, "List available models and exit")
	flag.BoolVar(&quotaFlag, "quota", false, "Inspect model quotas and rate-limit replenishment windows")
	flag.BoolVar(&prodFlag, "prod", false, "Use the production endpoint (cloudcode-pa.googleapis.com) instead of daily canary")
	flag.BoolVar(&rawFlag, "raw", false, "Expose raw backend model endpoints instead of compact canonical profiles")
	flag.Parse()

	ctx := context.Background()

	// 1. Setup Token Provider
	oauthProvider, err := loomantigravity.NewOAuth2TokenProvider(loomantigravity.OAuth2Config{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating OAuth provider: %v\n", err)
		os.Exit(1)
	}

	// Handle explicit login flag
	if loginFlag {
		fmt.Println("Initiating OAuth login flow...")
		if err := oauthProvider.Login(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Login successful! Token saved to disk.")
		if promptFlag == "" && !listModelsFlag {
			return
		}
	}

	// Determine token provider: check env var first, then OAuth token file
	var tokenProvider loomantigravity.TokenProvider = loomantigravity.NewEnvTokenProvider()
	if _, err := tokenProvider.Token(ctx); err != nil {
		// Env not set, check OAuth token file
		if _, err := oauthProvider.Token(ctx); err != nil {
			fmt.Println("No active credentials found.")
			fmt.Println("To authenticate, run:")
			fmt.Println("  go run ./examples/antigravity --login")
			fmt.Println("Or set an environment variable:")
			fmt.Println("  export ANTIGRAVITY_TOKEN=\"<your-token>\"")
			os.Exit(1)
		}
		tokenProvider = oauthProvider
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
		TokenProvider: tokenProvider,
		BaseURL:       baseURL,
		RawProfiles:   rawFlag,
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
		quotas, err := qp.ListQuotas(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching quotas: %v\n", err)
			os.Exit(1)
		}
		if len(quotas) == 0 {
			fmt.Println("No quota information reported by server.")
			return
		}
		for model, q := range quotas {
			resetStr := ""
			if q.ResetTime != "" {
				resetStr = fmt.Sprintf(" (resets at %s)", q.ResetTime)
			}
			fmt.Printf("• %-32s | Remaining: %5.1f%%%s\n", model, q.Percentage(), resetStr)
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
				case llm.ModalityImage:
					caps = append(caps, "Vision")
				case llm.ModalityVideo:
					caps = append(caps, "Video")
				case llm.ModalityPDF:
					caps = append(caps, "PDF")
				case llm.ModalityAudio:
					caps = append(caps, "Audio")
				}
			}

			quotaStr := ""
			if qp != nil {
				if q, ok, _ := qp.GetQuota(ctx, p.ID); ok {
					quotaStr = fmt.Sprintf(" | Quota: %4.1f%%", q.Percentage())
				}
			}

			fmt.Printf("• %-26s | %-32s (Ctx: %4dk, Out: %2dk)%s [%s]\n",
				p.ID, p.Name, p.Limits.Context/1024, p.Limits.Output/1024, quotaStr, strings.Join(caps, ", "))
		}
		fmt.Printf("\nTotal: %d models available.\n", len(profiles))
		return
	}

	// 5. Run interactive or single-prompt mode
	if promptFlag != "" {
		runPrompt(ctx, provider, modelFlag, promptFlag, effortFlag)
		return
	}

	// Interactive REPL loop
	fmt.Printf("Connected to Antigravity Provider (%s)\n", modelFlag)
	fmt.Println("Type your prompt and press Enter (or 'exit' to quit):")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		runPrompt(ctx, provider, modelFlag, input, effortFlag)
	}
}

func runPrompt(ctx context.Context, provider llm.Provider, modelName, prompt, effort string) {
	model, err := llm.NewModel(provider, modelName, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating model: %v\n", err)
		return
	}
	model = model.WithTemperature(0.7)
	if effort != "" {
		model = model.WithThinkingEffort(effort)
	} else {
		model = model.WithThinking(2048)
	}

	fmt.Printf("\n[%s]:\n", modelName)

	stream, err := model.Stream(ctx, []message.Message{
		message.NewUserText(prompt),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stream: %v\n", err)
		return
	}

	var thoughtStarted bool
	for chunk, err := range stream {
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nStream error: %v\n", err)
			return
		}

		for _, block := range chunk.Content {
			switch b := block.(type) {
			case *message.ThinkingBlock:
				if !thoughtStarted {
					fmt.Print("\033[90m[Thinking] ")
					thoughtStarted = true
				}
				fmt.Print(b.Thinking)
			case *message.TextBlock:
				if thoughtStarted {
					fmt.Print("\033[0m\n\n")
					thoughtStarted = false
				}
				fmt.Print(b.Text)
			}
		}

		if chunk.Done && chunk.Metrics != nil {
			if thoughtStarted {
				fmt.Print("\033[0m\n")
			}
			details := fmt.Sprintf("In=%d, Out=%d, Total=%d",
				chunk.Metrics.Tokens.Input,
				chunk.Metrics.Tokens.Output,
				chunk.Metrics.TotalTokens,
			)
			if chunk.Metrics.Tokens.Reasoning > 0 {
				details += fmt.Sprintf(", Reasoning=%d", chunk.Metrics.Tokens.Reasoning)
			}
			if chunk.Metrics.Tokens.CacheRead > 0 {
				details += fmt.Sprintf(", CacheRead=%d", chunk.Metrics.Tokens.CacheRead)
			}
			fmt.Printf("\n\n\033[90m[Done: %s | Tokens: %s]\033[0m\n", chunk.DoneReason, details)
		}
	}
}
