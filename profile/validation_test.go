package profile

import (
	"errors"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

func TestValidateContentModalities(t *testing.T) {
	t.Run("unprofiled model skips validation", func(t *testing.T) {
		var empty Profile
		content := message.Content{
			&message.ImageBlock{Data: []byte("img")},
			&message.AudioBlock{Data: []byte("aud")},
			&message.VideoBlock{Data: []byte("vid")},
			&message.DocumentBlock{Data: []byte("pdf")},
		}
		if err := empty.ValidateContentModalities("openai", "custom-vllm", content); err != nil {
			t.Fatalf("expected nil for empty profile, got: %v", err)
		}
	})

	t.Run("text-only model rejects image audio video document", func(t *testing.T) {
		textOnly := Profile{
			ID: "text-model",
			Modalities: Modalities{
				Inputs: []Modality{ModalityText},
			},
			Capabilities: Capabilities{Completion: true},
		}

		cases := []struct {
			name string
			blk  message.Block
			want Modality
		}{
			{"image", &message.ImageBlock{Data: []byte("x")}, ModalityImage},
			{"audio", &message.AudioBlock{Data: []byte("x")}, ModalityAudio},
			{"video", &message.VideoBlock{Data: []byte("x")}, ModalityVideo},
			{"pdf", &message.DocumentBlock{Data: []byte("x"), MIMEType: "application/pdf"}, ModalityPDF},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := textOnly.ValidateContentModalities("testprov", "text-model", message.Content{tc.blk})
				if !errors.Is(err, ErrUnsupportedModality) {
					t.Fatalf("expected ErrUnsupportedModality, got: %v", err)
				}
				var modErr *UnsupportedModalityError
				if !errors.As(err, &modErr) || modErr.Modality != tc.want {
					t.Fatalf("expected modality %q, got: %#v", tc.want, modErr)
				}
			})
		}
	})

	t.Run("multimodal model allows declared modalities", func(t *testing.T) {
		omni := Profile{
			ID: "omni",
			Modalities: Modalities{
				Inputs: []Modality{ModalityText, ModalityImage, ModalityPDF},
			},
			Capabilities: Capabilities{Completion: true, Attachment: true, ToolCall: true},
		}

		okContent := message.Content{
			&message.TextBlock{Text: "hi"},
			&message.ImageBlock{Data: []byte("img")},
			&message.DocumentBlock{Data: []byte("pdf"), MIMEType: "application/pdf"},
		}
		if err := omni.ValidateContentModalities("anthropic", "omni", okContent); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err := omni.ValidateContentModalities("anthropic", "omni", message.Content{
			&message.VideoBlock{Data: []byte("vid")},
		})
		if !errors.Is(err, ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality for video, got: %v", err)
		}
	})
}

func TestValidateToolCallCapability(t *testing.T) {
	var unknown Profile
	if err := unknown.ValidateToolCallCapability("openai", "custom", true); err != nil {
		t.Fatalf("expected nil for unknown profile, got: %v", err)
	}

	noTools := Profile{
		ID:           "no-tools",
		Capabilities: Capabilities{Completion: true, ToolCall: false},
		Modalities:   Modalities{Inputs: []Modality{ModalityText}},
	}
	if err := noTools.ValidateToolCallCapability("openai", "no-tools", false); err != nil {
		t.Fatalf("expected nil when hasTools=false, got: %v", err)
	}
	err := noTools.ValidateToolCallCapability("openai", "no-tools", true)
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected ErrUnsupportedCapability, got: %v", err)
	}
}
