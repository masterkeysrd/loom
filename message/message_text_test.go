package message

import "testing"

func TestMessageListTextRoundTrip(t *testing.T) {
	t.Parallel()

	original := MessageList{
		&Assistant{
			Base:    Base{ID: "assistant-1"},
			Content: Content{&TextBlock{Text: "hello"}},
		},
		&User{
			Base:    Base{ID: "user-1"},
			Content: Content{&TextBlock{Text: "hi"}},
		},
	}

	data, err := original.MarshalText()
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}

	var decoded MessageList
	if err := decoded.UnmarshalText(data); err != nil {
		t.Fatalf("unmarshal text: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("unexpected decoded length: got %d want %d", len(decoded), len(original))
	}
	if decoded[0].Role() != RoleAssistant || decoded[0].GetContent().Text() != "hello" {
		t.Fatalf("unexpected first message: role=%s text=%q", decoded[0].Role(), decoded[0].GetContent().Text())
	}
	if decoded[1].Role() != RoleUser || decoded[1].GetContent().Text() != "hi" {
		t.Fatalf("unexpected second message: role=%s text=%q", decoded[1].Role(), decoded[1].GetContent().Text())
	}
}
