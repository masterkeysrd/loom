package memory

import (
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	DefaultMessagesThreshold = 20
	DefaultTrimTokenLimit    = 4000
)

// defaultPromptTemplate is the Go template used when Config.PromptTemplate is
// empty. It receives a [promptData] value and exposes three fields:
//
//	{{.CurrentSummary}}   – the previous summary text, or empty on first call.
//	{{.NewMessages}}      – the conversation transcript of the new messages to fold in.
//	{{.MaxSummaryTokens}} – token budget hint (0 means unbounded).
const defaultPromptTemplate = `Context Extraction Assistant

# Primary Objective
Your sole objective in this task is to extract the highest quality/most relevant context from the conversation history below.

# Important Instructions
You're nearing the total number of input tokens you can accept, so you must extract the highest quality/most relevant pieces of information from your conversation history.
This context will then overwrite the conversation history presented below. Because of this, ensure the context you extract is only the most important information to continue working toward your overall goal.

The conversation history below will be replaced with the context you extract in this step.
You want to ensure that you don't repeat any actions you've already completed, so the context you extract from the conversation history should be focused on the most important information to your overall goal.

You should structure your summary using the following sections. Each section acts as a checklist - you must populate it with relevant information or explicitly state "None" if there is nothing to report for that section:

<formatting>
## SESSION INTENT
What is the user's primary goal or request? What overall task are you trying to accomplish? This should be concise but complete enough to understand the purpose of the entire session.

## SUMMARY
Extract and record all of the most important context from the conversation history. Include important choices, conclusions, or strategies determined during this conversation. Include the reasoning behind key decisions. Document any rejected options and why they were not pursued.

## ARTIFACTS
What artifacts, files, or resources were created, modified, or accessed during this conversation? For file modifications, list specific file paths and briefly describe the changes made to each. This section prevents silent loss of artifact information.

## NEXT STEPS
What specific tasks remain to be completed to achieve the session intent? What should you do next?
</formatting>

The user will message you with the full message history from which you'll extract context to create a replacement. Carefully read through it all and think deeply about what information is most important to your overall goal and should be saved:

With all of this in mind, please carefully read over the entire conversation history, and extract the most important and relevant context to replace it so that you can free up space in the conversation history.
Respond ONLY with the extracted context. Do not include any additional information, or text before or after the extracted context.

{{ if .CurrentSummary }}
Here is the current summary of the conversation history:

<current_summary>
{{ .CurrentSummary }}
</current_summary>
{{ end -}}

<messages>
Messages to summarize:
{{ .NewMessages }}
</messages>`

type Summarizer struct {
	invoker        llm.Invoker
	config         SummarizerConfig
	promptTemplate *template.Template
}

type SummarizerConfig struct {
	// PromptTemplate is the Go template used to generate the summarization prompt.
	PromptTemplate string

	// TokenCounter is used to estimate the token count of the conversation history.
	TokenCounter llm.TokenCounter

	// TrimTokensToSumarize is an optional token limit for the messages passed to the summarization prompt.
	TrimTokensToSumarize int

	// Triggers is a list of functions that determine whether summarization should be
	// performed based on the current conversation context. If any trigger returns true,
	// summarization will be executed.
	Triggers []SummarizerTrigger

	// Keep is a function that determines how many of the most recent messages to
	// keep in the conversation history when summarization is triggered.
	Keep SummarizerKeepFunc

	// ContextLimit is the maximum number of tokens allowed by the LLM model.
	// This is used by triggers like TriggerSummaryOnFraction to calculate
	// thresholds based on context window usage.
	ContextLimit int

	// Filter is an optional function that determines which messages should be
	// excluded from the summarization prompt.
	Filter SummarizerFilterFunc
}

type SummarizerFilterFunc func(message.Message) bool

func NewSummarizer(invoker llm.Invoker, config SummarizerConfig) (*Summarizer, error) {
	if invoker == nil {
		return nil, fmt.Errorf("an llm.Invoker implementation must be provided")
	}

	promptTmplStr := config.PromptTemplate
	if promptTmplStr == "" {
		promptTmplStr = defaultPromptTemplate
	}

	promptTmpl, err := template.New("summarization_prompt").Parse(promptTmplStr)
	if err != nil {
		return nil, err
	}

	if len(config.Triggers) == 0 {
		return nil, fmt.Errorf("at least one SummarizerTrigger must be provided")
	}

	if config.TokenCounter == nil {
		return nil, fmt.Errorf("a TokenCounter implementation must be provided")
	}

	return &Summarizer{
		invoker:        invoker,
		config:         config,
		promptTemplate: promptTmpl,
	}, nil
}

func (s *Summarizer) Summarize(ctx context.Context, in SummarizeInput) (SummarizeOutput, error) {
	ctx, span := telemetry.Start(ctx, "loom.memory.summarize", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	messages := in.Messages
	if err := s.ensureMessagesIds(messages); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return SummarizeOutput{}, fmt.Errorf("failed to ensure message IDs: %w", err)
	}

	totalTokens, err := s.config.TokenCounter.CountTokens(ctx, messages)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return SummarizeOutput{}, fmt.Errorf("failed to count tokens: %w", err)
	}

	shouldSummarize, err := s.shouldSummarize(ctx, in.Messages, totalTokens)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return SummarizeOutput{}, fmt.Errorf("failed to evaluate summarization triggers: %w", err)
	}

	if !shouldSummarize {
		return SummarizeOutput{
			Summary:  in.CurrentSummary,
			Messages: in.Messages,
			Tokens:   0,
		}, nil
	}

	startTime := time.Now()
	defer func() {
		telemetry.RecordMemorySummarizeDuration(ctx, time.Since(startTime))
	}()

	cutoffIdx, err := s.findCutoff(ctx, in)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return SummarizeOutput{}, fmt.Errorf("failed to determine cutoff for summarization: %w", err)
	}

	if cutoffIdx <= 0 {
		return SummarizeOutput{
			Summary:  in.CurrentSummary,
			Messages: in.Messages,
			Tokens:   0,
		}, nil
	}

	messagesToSummarize, messagesToKeep := s.partitionMessages(in.Messages, cutoffIdx)
	summary, err := s.createSummary(ctx, in.CurrentSummary, messagesToSummarize)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return SummarizeOutput{}, fmt.Errorf("failed to create summary: %w", err)
	}
	newMessages := s.buildNewMessages(summary)
	return SummarizeOutput{
		Summary:  summary,
		Messages: append(newMessages, messagesToKeep...),
		Tokens:   totalTokens,
	}, nil
}

func (s *Summarizer) ShouldSummarize(ctx context.Context, msgs message.MessageList) (bool, error) {
	totalTokens, err := s.config.TokenCounter.CountTokens(ctx, msgs)
	if err != nil {
		return false, fmt.Errorf("failed to count tokens: %w", err)
	}

	return s.shouldSummarize(ctx, msgs, totalTokens)
}

func (s *Summarizer) shouldSummarize(ctx context.Context, msgs message.MessageList, totalTokens int) (bool, error) {
	for _, trigger := range s.config.Triggers {
		triggered, err := trigger(ctx, SummarizerTriggerContext{
			Messages:     msgs,
			TotalTokens:  totalTokens,
			ContextLimit: s.config.ContextLimit,
		})
		if err != nil {
			return false, fmt.Errorf("failed to evaluate trigger: %w", err)
		}
		if triggered {
			return true, nil
		}
	}
	return false, nil
}

func (s *Summarizer) findCutoff(ctx context.Context, in SummarizeInput) (int, error) {
	keep, err := s.config.Keep(ctx, SummarizerKeepContext{
		Messages:     in.Messages,
		TokenCounter: s.config.TokenCounter,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to evaluate keep function: %w", err)
	}
	return keep, nil
}

func (s *Summarizer) partitionMessages(messages message.MessageList, cutoff int) (toSummarize, toKeep message.MessageList) {
	toSummarize = messages[:cutoff]
	toKeep = messages[cutoff:]

	return toSummarize, toKeep
}

func (s *Summarizer) createSummary(ctx context.Context, currentSummary string, messagesToSummarize message.MessageList) (string, error) {
	if len(messagesToSummarize) == 0 {
		return "No previous conversation history.", nil
	}

	// Filter out previous summary messages (internal safety) and apply
	// user-provided filters.
	filtered := make(message.MessageList, 0, len(messagesToSummarize))
	for _, m := range messagesToSummarize {
		if m.GetMetadata()["loom_src"] == "summarizer" {
			continue
		}
		if s.config.Filter != nil && s.config.Filter(m) {
			continue
		}
		filtered = append(filtered, m)
	}
	messagesToSummarize = filtered

	trimmedMessages, err := s.trimMessagesForSummary(ctx, messagesToSummarize)
	if err != nil {
		return "", fmt.Errorf("failed to trim messages for summary: %w", err)
	}

	if len(trimmedMessages) == 0 {
		return "Previous conversation was too long to summarize.", nil
	}

	formattedMessages, err := message.FormatMessages(trimmedMessages, nil)
	if err != nil {
		return "", fmt.Errorf("failed to format messages for summary: %w", err)
	}

	var promptBuf strings.Builder
	if err := s.promptTemplate.Execute(&promptBuf, promptData{
		CurrentSummary: currentSummary,
		NewMessages:    formattedMessages,
	}); err != nil {
		return "", fmt.Errorf("failed to execute summarization prompt template: %w", err)
	}

	resp, err := s.invoker.Invoke(ctx, message.MessageList{
		message.NewUserText(promptBuf.String()),
	})
	if err != nil {
		return "", fmt.Errorf("failed to invoke LLM for summarization: %w", err)
	}

	return strings.TrimSpace(resp.GetContent().Text()), nil
}

func (s *Summarizer) trimMessagesForSummary(ctx context.Context, messages message.MessageList) (message.MessageList, error) {
	if s.config.TrimTokensToSumarize <= 0 {
		return messages, nil
	}
	messages, err := message.TrimMessages(ctx, messages, s.config.TrimTokensToSumarize, &message.TrimConfig{
		CountTokens:   s.config.TokenCounter.CountTokens,
		StartOn:       []message.Role{message.RoleUser},
		Strategy:      message.TrimStrategyLast,
		AllowPartial:  true,
		IncludeSystem: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to trim messages for summarization: %w", err)
	}

	return messages, nil

}

func (s *Summarizer) buildNewMessages(summary string) message.MessageList {
	text := "Summary of previous conversation history: \n" + summary
	return message.MessageList{
		message.NewUserTextMeta(text, map[string]any{
			"loom_src": "summarizer",
		}),
	}
}

func (s *Summarizer) ensureMessagesIds(messages message.MessageList) error {
	for i := range messages {
		if messages[i].GetID() == "" {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("failed to generate message ID: %w", err)
			}
			messages[i].SetID(id.String())
		}
	}

	return nil
}

type SummarizeInput struct {
	CurrentSummary string
	Messages       message.MessageList
}

type SummarizeOutput struct {
	Summary  string
	Messages message.MessageList
	Tokens   int
}

type promptData struct {
	CurrentSummary   string
	NewMessages      string
	MaxSummaryTokens int
}
