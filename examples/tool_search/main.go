package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomanthropic "github.com/masterkeysrd/loom/providers/anthropic"
	loomollama "github.com/masterkeysrd/loom/providers/ollama"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/tool"
)

func main() {
	var (
		providerFlag string
		modelFlag    string
		searchFlag   string
		queryFlag    string
	)
	flag.StringVar(&providerFlag, "provider", "ollama", "Provider to use: ollama, anthropic, or openai")
	flag.StringVar(&modelFlag, "model", "", "Model to use (default: qwen3.6:35b-mlx for ollama, claude-sonnet-4-6 for anthropic, gpt-5.4 for openai)")
	flag.StringVar(&searchFlag, "search", "bm25", "Search strategy: bm25, regex, or server")
	flag.StringVar(&queryFlag, "query", "", "Optional single-shot query string (if empty, starts an interactive REPL)")
	flag.Parse()

	providerFlag = strings.ToLower(strings.TrimSpace(providerFlag))
	searchFlag = strings.ToLower(strings.TrimSpace(searchFlag))

	var p llm.Provider
	var defaultModel string

	switch providerFlag {
	case "ollama":
		defaultModel = "qwen3.6:35b-mlx"
		prov, err := loomollama.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create ollama provider: %v", err)
		}
		p = prov
	case "anthropic":
		defaultModel = "claude-sonnet-4-6"
		prov, err := loomanthropic.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create anthropic provider: %v", err)
		}
		p = prov
	case "openai":
		defaultModel = "gpt-5.4"
		prov, err := loomopenai.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create openai provider: %v", err)
		}
		p = prov
	default:
		log.Fatalf("unsupported provider %q: choose 'ollama', 'anthropic', or 'openai'", providerFlag)
	}

	if modelFlag == "" {
		modelFlag = defaultModel
	}

	if searchFlag == "server" && providerFlag == "ollama" {
		log.Fatalf("ollama does not support server-side tool search (-search=server); use -search=bm25 or -search=regex")
	}

	// Enable content recording to capture raw messages and tool payloads in Loom Studio
	ctx := telemetry.WithContentRecording(context.Background())

	// Initialize Telemetry
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "tool-search-example",
		Insecure:    true,
	})
	if err != nil {
		log.Printf("Warning: failed to initialize telemetry: %v", err)
	} else {
		defer shutdown(ctx)
	}

	// 1. Define 5 realistic deferred tools with tool.WithDeferLoading()
	weatherTool, err := tool.New(
		"get_weather",
		"Current Weather",
		"Returns current weather conditions and temperature for a given city.",
		func(ctx context.Context, in struct {
			City string `json:"city" jsonschema:"The name of the city, e.g. 'San Francisco, CA' or 'Tokyo'"`
		}) (string, error) {
			city := strings.ToLower(in.City)
			switch {
			case strings.Contains(city, "san francisco"):
				return "San Francisco: 62°F (17°C), Foggy with light ocean breeze", nil
			case strings.Contains(city, "tokyo"):
				return "Tokyo: 20°C (68°F), Clear skies and pleasant", nil
			case strings.Contains(city, "santo domingo"):
				return "Santo Domingo: 86°F (30°C), Sunny and tropical", nil
			case strings.Contains(city, "london"):
				return "London: 14°C (57°F), Overcast with occasional drizzle", nil
			default:
				return fmt.Sprintf("%s: 72°F (22°C), Partly cloudy", in.City), nil
			}
		},
		tool.WithDeferLoading(),
	)
	if err != nil {
		log.Fatalf("failed to create weather tool: %v", err)
	}

	currencyTool, err := tool.New(
		"convert_currency",
		"Currency Converter",
		"Converts an amount from a source currency to a target currency (supports USD, EUR, JPY, GBP, DOP).",
		func(ctx context.Context, in struct {
			Amount float64 `json:"amount" jsonschema:"The amount of money to convert"`
			From   string  `json:"from" jsonschema:"The 3-letter source currency code (e.g. USD, EUR, JPY, GBP, DOP)"`
			To     string  `json:"to" jsonschema:"The 3-letter target currency code (e.g. USD, EUR, JPY, GBP, DOP)"`
		}) (string, error) {
			ratesToUSD := map[string]float64{
				"USD": 1.0,
				"EUR": 1.08,
				"GBP": 1.28,
				"JPY": 0.0067,
				"DOP": 0.017,
			}
			from := strings.ToUpper(strings.TrimSpace(in.From))
			to := strings.ToUpper(strings.TrimSpace(in.To))
			fromRate, okFrom := ratesToUSD[from]
			toRate, okTo := ratesToUSD[to]
			if !okFrom {
				return "", fmt.Errorf("unsupported source currency %q", from)
			}
			if !okTo {
				return "", fmt.Errorf("unsupported target currency %q", to)
			}
			inUSD := in.Amount * fromRate
			converted := inUSD / toRate
			return fmt.Sprintf("%.2f %s = %.2f %s", in.Amount, from, converted, to), nil
		},
		tool.WithDeferLoading(),
	)
	if err != nil {
		log.Fatalf("failed to create currency tool: %v", err)
	}

	mortgageTool, err := tool.New(
		"calculate_mortgage",
		"Mortgage Calculator",
		"Computes monthly mortgage payments, total payment, and total interest given principal, annual interest rate, and term in years.",
		func(ctx context.Context, in struct {
			Principal float64 `json:"principal" jsonschema:"Loan principal amount in dollars"`
			Rate      float64 `json:"annual_interest_rate" jsonschema:"Annual interest rate as a percentage, e.g. 6.5 for 6.5%"`
			Years     int     `json:"years" jsonschema:"Loan term in years, e.g. 15 or 30"`
		}) (string, error) {
			if in.Principal <= 0 || in.Years <= 0 {
				return "", fmt.Errorf("principal and years must be positive")
			}
			monthlyRate := (in.Rate / 100.0) / 12.0
			nMonths := float64(in.Years * 12)
			var monthlyPayment float64
			if monthlyRate == 0 {
				monthlyPayment = in.Principal / nMonths
			} else {
				compound := math.Pow(1+monthlyRate, nMonths)
				monthlyPayment = in.Principal * (monthlyRate * compound) / (compound - 1)
			}
			totalPayment := monthlyPayment * nMonths
			totalInterest := totalPayment - in.Principal
			return fmt.Sprintf("Monthly Payment: $%.2f | Total Payment: $%.2f | Total Interest: $%.2f (Loan: $%.2f, Rate: %.2f%%, Term: %d yrs)",
				monthlyPayment, totalPayment, totalInterest, in.Principal, in.Rate, in.Years), nil
		},
		tool.WithDeferLoading(),
	)
	if err != nil {
		log.Fatalf("failed to create mortgage tool: %v", err)
	}

	employeeTool, err := tool.New(
		"query_employees",
		"Employee Directory",
		"Searches employee directory by department (engineering, product, sales, marketing) or role keyword.",
		func(ctx context.Context, in struct {
			Department string `json:"department,omitempty" jsonschema:"Department to filter by, e.g. 'engineering', 'sales'"`
			Query      string `json:"query,omitempty" jsonschema:"Keyword to match against name or role, e.g. 'director', 'frontend'"`
		}) (string, error) {
			type emp struct {
				Name string
				Role string
				Dept string
			}
			directory := []emp{
				{Name: "Elena Rostova", Role: "Principal Distributed Systems Engineer", Dept: "engineering"},
				{Name: "Marcus Vance", Role: "Staff AI/ML Researcher", Dept: "engineering"},
				{Name: "Sarah Chen", Role: "VP of Product Management", Dept: "product"},
				{Name: "David Kim", Role: "Senior Product Designer", Dept: "product"},
				{Name: "Amara Diallo", Role: "Global Enterprise Sales Director", Dept: "sales"},
				{Name: "Carlos Mendez", Role: "Technical Solutions Architect", Dept: "sales"},
				{Name: "Chloe Dupont", Role: "Head of Developer Relations & Marketing", Dept: "marketing"},
			}
			deptFilter := strings.ToLower(strings.TrimSpace(in.Department))
			qFilter := strings.ToLower(strings.TrimSpace(in.Query))

			var matches []string
			for _, e := range directory {
				if deptFilter != "" && !strings.Contains(strings.ToLower(e.Dept), deptFilter) {
					continue
				}
				if qFilter != "" && !strings.Contains(strings.ToLower(e.Name), qFilter) && !strings.Contains(strings.ToLower(e.Role), qFilter) {
					continue
				}
				matches = append(matches, fmt.Sprintf("- %s (%s, Dept: %s)", e.Name, e.Role, e.Dept))
			}
			if len(matches) == 0 {
				return "No employees matching criteria found.", nil
			}
			return fmt.Sprintf("Found %d employees:\n%s", len(matches), strings.Join(matches, "\n")), nil
		},
		tool.WithDeferLoading(),
	)
	if err != nil {
		log.Fatalf("failed to create employee tool: %v", err)
	}

	shellTool, err := tool.New(
		"shell_command",
		"Shell Command Executor",
		"Executes safe basic shell commands: ls, pwd, date, echo, uname.",
		func(ctx context.Context, in struct {
			Command string   `json:"command" jsonschema:"Safe command name: ls, pwd, date, echo, uname"`
			Args    []string `json:"args,omitempty" jsonschema:"Arguments for the command"`
		}) (string, error) {
			allowed := map[string]bool{
				"ls":    true,
				"pwd":   true,
				"date":  true,
				"echo":  true,
				"uname": true,
			}
			if !allowed[in.Command] {
				return "", fmt.Errorf("command %q is not permitted; allowed commands are: ls, pwd, date, echo, uname", in.Command)
			}
			cmd := exec.CommandContext(ctx, in.Command, in.Args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("command execution failed: %w", err)
			}
			return strings.TrimSpace(string(out)), nil
		},
		tool.WithDeferLoading(),
	)
	if err != nil {
		log.Fatalf("failed to create shell tool: %v", err)
	}

	deferredTools := []*tool.Tool{
		weatherTool,
		currencyTool,
		mortgageTool,
		employeeTool,
		shellTool,
	}

	// 2. Setup Search Tool & Container
	var allTools []*tool.Tool
	var container *tool.Container

	model, err := llm.NewModel(p, modelFlag, nil)
	if err != nil {
		log.Fatalf("failed to create model: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))

	switch searchFlag {
	case "server":
		container = tool.NewContainer(deferredTools...)
		model = model.BindTools(deferredTools...)
		if providerFlag == "anthropic" {
			model = model.WithExtension(loomanthropic.ToolSearch{Strategy: loomanthropic.ToolSearchBM25})
		} else if providerFlag == "openai" {
			model = model.WithExtension(loomopenai.WithToolSearch())
		}
	case "regex":
		searchTool, err := tool.NewRegexSearch(deferredTools, tool.WithSearchTopK(3))
		if err != nil {
			log.Fatalf("failed to create regex search tool: %v", err)
		}
		allTools = append([]*tool.Tool{searchTool}, deferredTools...)
		container = tool.NewContainer(allTools...)
		model = model.BindTools(allTools...)
	case "bm25":
		fallthrough
	default:
		searchTool, err := tool.NewBM25Search(deferredTools, tool.WithSearchTopK(3))
		if err != nil {
			log.Fatalf("failed to create bm25 search tool: %v", err)
		}
		allTools = append([]*tool.Tool{searchTool}, deferredTools...)
		container = tool.NewContainer(allTools...)
		model = model.BindTools(allTools...)
	}

	systemPrompt := `You are an AI assistant equipped with deferred tools.
Most tools are not listed in your initial <tools> header to keep the prompt compact.
1. When asked to perform a task for which you do not yet have the tool schema, call the tool search tool ONCE to discover it.
2. IMPORTANT: As soon as a tool definition is returned in a tool result (or system message), that tool IS IMMEDIATELY ACTIVE AND CALLABLE in this session. You MUST invoke it directly by its name as a standard tool call, even though it does not appear in your initial <tools> list. Never tell the user a loaded tool is unavailable, and never search for the same tool twice.`

	messages := []message.Message{
		message.NewSystemText(systemPrompt),
	}

	executeTurn := func(turnCtx context.Context, userInput string) {
		messages = append(messages, message.NewUserText(userInput))

		for {
			resp, err := model.Invoke(turnCtx, messages)
			if err != nil {
				fmt.Printf("\nError: %v\n", err)
				break
			}
			messages = append(messages, resp)

			// Print any server-side tool calls/results (native Anthropic/OpenAI)
			for _, block := range resp.GetContent() {
				switch b := block.(type) {
				case *message.ServerToolCall:
					fmt.Printf("\n[Server Tool Call: %s (id: %s) args: %v]\n", b.Name, b.ID, b.Args)
				case *message.ServerToolResult:
					fmt.Printf("\n[Server Tool Result: %s (id: %s) is_error: %v]\n", b.Name, b.ToolCallID, b.IsError)
					for _, sub := range b.Content {
						if ref, ok := sub.(*message.ToolReferenceBlock); ok {
							fmt.Printf("  -> [Discovered tool: %s]\n", ref.ToolName)
						}
					}
				}
			}

			// Print assistant text
			if text := resp.GetContent().Text(); text != "" {
				fmt.Printf("\nAssistant: %s\n", text)
			}

			// Check for client-side tool calls
			var toolCalls []*message.ToolCall
			for _, block := range resp.GetContent() {
				if tc, ok := block.(*message.ToolCall); ok {
					toolCalls = append(toolCalls, tc)
				}
			}

			if len(toolCalls) == 0 {
				break
			}

			// Execute client tool calls
			for _, tc := range toolCalls {
				fmt.Printf("\n[Executing %s with args %v...]\n", tc.Name, tc.Args)
				toolResp, err := container.Call(turnCtx, tc)
				if err != nil {
					fmt.Printf("Tool Error: %v\n", err)
					messages = append(messages, &message.Tool{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						IsError:    true,
						Content:    message.Content{&message.TextBlock{Text: err.Error()}},
					})
				} else {
					// Print discovered tools if tool search returned ToolReferenceBlocks
					for _, b := range toolResp.Content {
						if ref, ok := b.(*message.ToolReferenceBlock); ok {
							fmt.Printf("[Discovered tool: %s]\n", ref.ToolName)
						}
					}
					if toolResp.Content.Text() != "" {
						fmt.Printf("[Tool Output: %s]\n", toolResp.Content.Text())
					}
					messages = append(messages, toolResp)
				}
			}
		}
	}

	// Single-shot mode if -query was provided
	if queryFlag != "" {
		turnCtx, span := telemetry.Start(ctx, "tool-search-query")
		defer span.End()
		executeTurn(turnCtx, queryFlag)
		return
	}

	// Interactive REPL Mode
	fmt.Println("=== Loom Deferred Tools & Tool Search REPL ===")
	fmt.Printf("Provider: %s | Model: %s | Search Strategy: %s\n", providerFlag, modelFlag, searchFlag)
	fmt.Println("Available Deferred Tools:")
	fmt.Println("  - get_weather        (Current weather & temperature for a city)")
	fmt.Println("  - convert_currency   (USD, EUR, JPY, GBP, DOP converter)")
	fmt.Println("  - calculate_mortgage (Loan payment & interest calculator)")
	fmt.Println("  - query_employees    (Employee directory by department or role)")
	fmt.Println("  - shell_command      (Safe shell commands: ls, pwd, date, echo, uname)")
	fmt.Println("\nCommands:")
	fmt.Println("  - Type any prompt (e.g. 'What is the weather in Tokyo and convert 100 USD to EUR?')")
	fmt.Println("  - Type 'activate <tool_name>' or 'activate:<tool_name>' to manually inject a mid-turn ToolReferenceBlock")
	fmt.Println("  - Type 'exit' or 'quit' to quit")

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
		if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			break
		}

		if strings.HasPrefix(input, "activate:") || strings.HasPrefix(input, "activate ") {
			var toolName string
			if after, ok := strings.CutPrefix(input, "activate:"); ok {
				toolName = strings.TrimSpace(after)
			} else {
				toolName = strings.TrimSpace(strings.TrimPrefix(input, "activate "))
			}
			messages = append(messages, message.NewSystem(&message.ToolReferenceBlock{ToolName: toolName}))
			fmt.Printf("[Manually activated tool %q mid-conversation via ToolReferenceBlock]\n", toolName)
			continue
		}

		turnCtx, span := telemetry.Start(ctx, "tool-search-turn")
		executeTurn(turnCtx, input)
		span.End()
	}
}
