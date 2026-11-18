package profile

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/message"
)

var (
	// ErrUnsupportedModality indicates that a request includes an input/output
	// media modality (or delivery mode such as URL vs inline bytes) not supported
	// by the target model profile or provider wire format.
	ErrUnsupportedModality = errors.New("unsupported modality")

	// ErrUnsupportedCapability indicates that a request relies on a capability
	// (such as tool_call) that the known model profile does not support.
	ErrUnsupportedCapability = errors.New("unsupported capability")
)

// UnsupportedModalityError describes a modality or media-source rejection for a
// specific provider and model. It wraps [ErrUnsupportedModality].
type UnsupportedModalityError struct {
	Provider string
	Model    string
	Modality Modality
	Detail   string
}

func (e *UnsupportedModalityError) Error() string {
	target := e.Provider
	if e.Model != "" {
		if target != "" {
			target = fmt.Sprintf("%s model %q", e.Provider, e.Model)
		} else {
			target = fmt.Sprintf("model %q", e.Model)
		}
	}
	if target == "" {
		target = "provider"
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s does not support %s modality: %s", target, e.Modality, e.Detail)
	}
	return fmt.Sprintf("%s does not support %s modality", target, e.Modality)
}

func (e *UnsupportedModalityError) Unwrap() error {
	return ErrUnsupportedModality
}

// NewUnsupportedModalityError constructs an [*UnsupportedModalityError].
func NewUnsupportedModalityError(provider, model string, modality Modality, detail string) *UnsupportedModalityError {
	return &UnsupportedModalityError{
		Provider: provider,
		Model:    model,
		Modality: modality,
		Detail:   detail,
	}
}

// UnsupportedCapabilityError describes a capability rejection against a known
// model profile. It wraps [ErrUnsupportedCapability].
type UnsupportedCapabilityError struct {
	Provider   string
	Model      string
	Capability string
	Detail     string
}

func (e *UnsupportedCapabilityError) Error() string {
	target := e.Provider
	if e.Model != "" {
		if target != "" {
			target = fmt.Sprintf("%s model %q", e.Provider, e.Model)
		} else {
			target = fmt.Sprintf("model %q", e.Model)
		}
	}
	if target == "" {
		target = "provider"
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s does not support capability %q: %s", target, e.Capability, e.Detail)
	}
	return fmt.Sprintf("%s does not support capability %q", target, e.Capability)
}

func (e *UnsupportedCapabilityError) Unwrap() error {
	return ErrUnsupportedCapability
}

// NewUnsupportedCapabilityError constructs an [*UnsupportedCapabilityError].
func NewUnsupportedCapabilityError(provider, model, capability, detail string) *UnsupportedCapabilityError {
	return &UnsupportedCapabilityError{
		Provider:   provider,
		Model:      model,
		Capability: capability,
		Detail:     detail,
	}
}

// HasInputModalities reports whether the profile explicitly declares supported input modalities.
func (p Profile) HasInputModalities() bool {
	return len(p.Modalities.Inputs) > 0
}

// SupportsInputModality reports whether m is listed in p.Modalities.Inputs.
func (p Profile) SupportsInputModality(m Modality) bool {
	return slices.Contains(p.Modalities.Inputs, m)
}

func (p Profile) hasKnownCapabilities() bool {
	c := p.Capabilities
	return c.Completion || c.Decision || c.Embedding || c.Attachment || c.Reasoning || c.ToolCall || c.Temperature || len(p.Modalities.Inputs) > 0 || len(p.Modalities.Outputs) > 0
}

// ValidateContentModalities checks all media/content blocks in content against
// p.Modalities.Inputs when p.HasInputModalities() is true. If p declares no
// input modalities (unknown/unprofiled model), nil is returned.
func (p Profile) ValidateContentModalities(provider, model string, content message.Content) error {
	if !p.HasInputModalities() {
		return nil
	}

	for _, block := range content {
		switch b := block.(type) {
		case *message.TextBlock:
			if b != nil && b.Text != "" && !p.SupportsInputModality(ModalityText) {
				return NewUnsupportedModalityError(provider, model, ModalityText, "model profile does not list text input modality")
			}
		case *message.ImageBlock:
			if !p.SupportsInputModality(ModalityImage) {
				return NewUnsupportedModalityError(provider, model, ModalityImage, "model profile does not list image input modality")
			}
		case *message.AudioBlock:
			if !p.SupportsInputModality(ModalityAudio) {
				return NewUnsupportedModalityError(provider, model, ModalityAudio, "model profile does not list audio input modality")
			}
		case *message.VideoBlock:
			if !p.SupportsInputModality(ModalityVideo) {
				return NewUnsupportedModalityError(provider, model, ModalityVideo, "model profile does not list video input modality")
			}
		case *message.DocumentBlock:
			if b != nil && isPlainTextDocumentMIME(b.MIMEType) && p.Capabilities.Attachment && p.SupportsInputModality(ModalityText) {
				continue
			}
			if !p.SupportsInputModality(ModalityPDF) {
				return NewUnsupportedModalityError(provider, model, ModalityPDF, "model profile does not list pdf/document input modality")
			}
		}
	}

	return nil
}

// ValidateMessagesModalities validates all messages in msgs against p.Modalities.Inputs
// when p.HasInputModalities() is true.
func (p Profile) ValidateMessagesModalities(provider, model string, msgs []message.Message) error {
	if !p.HasInputModalities() {
		return nil
	}
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		if err := p.ValidateContentModalities(provider, model, msg.GetContent()); err != nil {
			return fmt.Errorf("message[%d] (%s): %w", i, msg.Role(), err)
		}
	}
	return nil
}

// ValidateToolCallCapability checks that p.Capabilities.ToolCall is true when
// hasTools is true and p carries known capability metadata.
func (p Profile) ValidateToolCallCapability(provider, model string, hasTools bool) error {
	if !hasTools || !p.hasKnownCapabilities() {
		return nil
	}
	if !p.Capabilities.ToolCall {
		return NewUnsupportedCapabilityError(provider, model, "tool_call", "model profile has ToolCall=false")
	}
	return nil
}

func isPlainTextDocumentMIME(mimeType string) bool {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if idx := strings.IndexByte(mt, ';'); idx >= 0 {
		mt = strings.TrimSpace(mt[:idx])
	}
	return strings.HasPrefix(mt, "text/")
}
