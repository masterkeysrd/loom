package message

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMultimodalBlocksRoundTrip(t *testing.T) {
	original := MessageList{
		&User{
			Content: Content{
				&TextBlock{Text: "Look at this image:"},
				&ImageBlock{
					Data:     []byte("fake-image-data"),
					MIMEType: "image/png",
				},
			},
		},
		&Assistant{
			Content: Content{
				&TextBlock{Text: "I see it. Here is a document:"},
				&DocumentBlock{
					URL:      "https://example.com/doc.pdf",
					MIMEType: "application/pdf",
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded MessageList
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
	}

	for i := range original {
		if original[i].Role() != decoded[i].Role() {
			t.Errorf("role mismatch at index %d: got %s, want %s", i, decoded[i].Role(), original[i].Role())
		}

		origContent := original[i].GetContent()
		decContent := decoded[i].GetContent()

		if len(origContent) != len(decContent) {
			t.Errorf("content length mismatch at index %d: got %d, want %d", i, len(decContent), len(origContent))
			continue
		}

		for j := range origContent {
			if origContent[j].Kind() != decContent[j].Kind() {
				t.Errorf("block kind mismatch at index %d,%d: got %s, want %s", i, j, decContent[j].Kind(), origContent[j].Kind())
			}

			// Deep equal check for the blocks
			if !reflect.DeepEqual(origContent[j], decContent[j]) {
				t.Errorf("block content mismatch at index %d,%d", i, j)
			}
		}
	}
}

func TestCloneMultimodal(t *testing.T) {
	img := &ImageBlock{
		Data:     []byte("data"),
		MIMEType: "image/png",
		Extras:   map[string]any{"key": "value"},
	}

	cloned := CloneBlock(img).(*ImageBlock)

	if cloned == img {
		t.Fatal("clone returned same pointer")
	}

	if !reflect.DeepEqual(cloned.Data, img.Data) {
		t.Error("data mismatch")
	}

	// Ensure data is deeply cloned
	cloned.Data[0] = 'X'
	if img.Data[0] == 'X' {
		t.Error("data not deeply cloned")
	}

	if !reflect.DeepEqual(cloned.Extras, img.Extras) {
		t.Error("extras mismatch")
	}

	cloned.Extras["key"] = "new"
	if img.Extras["key"] == "new" {
		t.Error("extras not deeply cloned")
	}
}

func TestFormatMultimodal(t *testing.T) {
	msg := &User{
		Content: Content{
			&ImageBlock{URL: "http://img.jpg", MIMEType: "image/jpeg"},
			&AudioBlock{Data: []byte("audio"), MIMEType: "audio/mpeg"},
		},
	}

	prefix, _ := FormatMessages(MessageList{msg}, nil)
	if !reflect.DeepEqual(prefix, "User: [Image: http://img.jpg]\n[Audio: audio/mpeg (5 bytes)]\n") {
		t.Errorf("prefix format mismatch: %q", prefix)
	}

	xml, _ := FormatMessages(MessageList{msg}, &FormatOptions{FormatType: FormatTypeXML})
	if !reflect.DeepEqual(xml, "<message role=\"User\">\n  <content>\n<image url=\"http://img.jpg\" mime_type=\"image/jpeg\" /><audio mime_type=\"audio/mpeg\" data_length=\"5\" />  </content>\n</message>\n") {
		t.Errorf("xml format mismatch: %q", xml)
	}
}
