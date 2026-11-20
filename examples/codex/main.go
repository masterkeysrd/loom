// Command codex runs a Loom tool-calling graph using Sign in with ChatGPT.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/masterkeysrd/loom/examples/internal/agentrepl"
	"github.com/masterkeysrd/loom/llm"
	loomcodex "github.com/masterkeysrd/loom/providers/codex"
	"github.com/masterkeysrd/loom/tool"
)

func main() {
	login := flag.Bool("login", false, "Register or reauthorize this example in your browser")
	list := flag.Bool("list-models", false, "List model IDs available to the signed-in account")
	id := flag.String("model", os.Getenv("CODEX_MODEL"), "Model ID (defaults to CODEX_MODEL or the first visible account model)")
	prompt := flag.String("prompt", "", "Run one prompt (omit for interactive chat)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *login, *list, *id, *prompt); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type savedSession struct {
	HostID      string                 `json:"host_id"`
	Credentials *loomcodex.Credentials `json:"credentials,omitempty"`
}

func run(ctx context.Context, login, list bool, id, prompt string) error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "loom", "codex-example", "session.json")
	var session savedSession
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &session); err != nil {
			return fmt.Errorf("read session: %w", err)
		}
		if session.HostID == "" {
			return errors.New("saved session is missing its host ID")
		}
	case errors.Is(err, os.ErrNotExist):
		session.HostID = loomcodex.NewHostID()
		if err := writeSession(path, session); err != nil {
			return err
		}
	default:
		return err
	}
	save := func(c loomcodex.Credentials) error {
		session.Credentials = &c
		return writeSession(path, session)
	}
	if login || session.Credentials == nil {
		c, err := loomcodex.Login(ctx, loomcodex.LoginOptions{
			AgentName: "Loom Codex Example", HostID: session.HostID, Previous: session.Credentials,
		})
		if err != nil {
			return err
		}
		if err := save(*c); err != nil {
			return err
		}
		fmt.Println("Signed in.")
		if login && !list && id == "" && prompt == "" {
			return nil
		}
	}
	source, err := loomcodex.NewTokenSource(ctx, session.Credentials, save)
	if err != nil {
		return err
	}
	provider, err := loomcodex.NewProvider(source, nil)
	if err != nil {
		return err
	}
	if list || id == "" {
		models, err := provider.ListModels(ctx)
		if err != nil {
			return err
		}
		if len(models) == 0 {
			return errors.New("no models are visible to the signed-in ChatGPT account")
		}
		if list {
			for _, model := range models {
				fmt.Println(model.ID)
			}
			return nil
		}
		id = models[0].ID
	}
	model, err := llm.NewModel(provider, id, nil)
	if err != nil {
		return err
	}
	shellTool, err := agentrepl.NewShellTool()
	if err != nil {
		return err
	}
	return agentrepl.Run(ctx, model, agentrepl.Options{
		Title:        fmt.Sprintf("Loom Codex REPL (%s)", id),
		GraphName:    "CodexAgent",
		SystemPrompt: "You are a helpful coding assistant. Use shell_command only when needed.",
		Tools:        []*tool.Tool{shellTool},
		Stream:       true,
		Prompt:       prompt,
	})
}

// Replace the single-account session only after the new private file is complete.
func writeSession(path string, session savedSession) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(session); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
