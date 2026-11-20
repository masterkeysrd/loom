---
title: "Codex with Sign in with ChatGPT"
---

Use `providers/codex` to call the Responses API directly with an eligible ChatGPT
plan. You need a browser for the first sign-in. Loom sends the conversation and
tool definitions on each model call, so you can run your graph without installing
the Codex CLI. [OpenAI’s direct inference guide](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)

## Run the example

From the repository root, start the interactive coding assistant. It opens a
browser on first use, then reuses the saved registration on later runs.

```sh
go run ./examples/codex
```

To run one turn, pass a prompt. The shared `shell_command` tool lets the
assistant inspect the current directory.

```sh
go run ./examples/codex -prompt "Use ls to list the files in this directory."
```

The example selects the first visible model in the account catalog by default.
Set `CODEX_MODEL` to choose a default or pass `-model` for one run. To see
available IDs and start a session with a particular model, run the following commands.

```sh
go run ./examples/codex -list-models
go run ./examples/codex -model MODEL_ID
```

The example keeps one account registration in `loom/codex-example/session.json`
under the OS user configuration directory. Run one instance at a time so a
single process owns token refresh. Use `-login` again to reauthorize the saved
account. Chat history lasts for the current process. [Example source](https://github.com/masterkeysrd/loom/blob/main/examples/codex/main.go)

## Register and sign in

Use this integration in eligible open-source and locally hosted applications.
Generate a host ID with `loomcodex.NewHostID()` once and save it before starting
sign-in. Keep each account registration in a separate protected record keyed by
the issued client ID. Persist credentials atomically with owner-only permissions
(`0600` on Unix). Pass a saved registration in `LoginOptions.Previous` to
reauthorize it. [Registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)

The following function runs first-time sign-in and builds a provider. The
application supplies a stable host ID and a function that saves the selected
registration. Pass an application-lifetime context for the refresh source.

```go
import (
    "context"

    loomcodex "github.com/masterkeysrd/loom/providers/codex"
)

func connectCodex(
    ctx context.Context,
    hostID string,
    save func(loomcodex.Credentials) error,
) (*loomcodex.Provider, error) {
    creds, err := loomcodex.Login(ctx, loomcodex.LoginOptions{
        AgentName: "My Loom App",
        HostID:    hostID,
    })
    if err != nil {
        return nil, err
    }
    if err := save(*creds); err != nil {
        return nil, err
    }
    source, err := loomcodex.NewTokenSource(ctx, creds, save)
    if err != nil {
        return nil, err
    }
    return loomcodex.NewProvider(source, nil)
}
```

On subsequent runs, load the selected credential record and call
`NewTokenSource` directly. It renews access near expiry and saves the rotated
token set before returning the refreshed access token. Use one source per
registration and coordinate refreshes if multiple processes share a credential
record. Applications that already manage direct ChatGPT OAuth can pass their
existing `oauth2.TokenSource` to `NewProvider`. [Accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)

## Choose a model and bind tools

`provider.ListModels(ctx)` returns the visible model slugs for the selected
account. Pass the desired profile's `ID` to
`llm.NewModel(provider, modelID, nil)`. Use `model.BindTools(myTool)` and the
usual Loom tool loop. If your graph checkpoints a pending call, persist the full
assistant message. On resume, execute the tool by name with its saved arguments
and attach the original call ID to the tool result. Send the restored history
and tool result together on the next model invocation.

Model discovery is cached per provider for one hour. Pass
`loomcodex.WithProfileCacheTTL(10*time.Minute)` as the third argument to
`NewProvider` to change the lifetime, or pass a zero duration to disable caching.
`ListModels` and `GetModel` refresh expired discovery on demand. The synchronous
`GetProfile`, `ListProfiles`, and `SearchProfiles` methods read fresh cached
metadata and explicit overrides without contacting the service. Overrides take
precedence and persist across refreshes.

For visible model IDs, Loom enriches account metadata with exact matches from
its OpenAI catalog and applies the ChatGPT route’s capability limits. Newly
listed models can receive bound tools before a static profile becomes available.

Each inference request uses streaming, sends the conversation history, and sets
`store: false`. Loom preserves encrypted reasoning items in the serialized
assistant message so the next request can include them.

## Preview limits

The provider omits `MaxTokens`, `Temperature`, and `TopP` on this route.
Function tools are supplied as Responses `additional_tools`. Hosted file
search, Code Interpreter, tool search, and audio or video inputs are unsupported.
Web search availability depends on the model and account policy.
[Responses API preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
