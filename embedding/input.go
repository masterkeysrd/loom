package embedding

import "github.com/masterkeysrd/loom/message"

// TaskType specifies the intended downstream application of the generated embeddings,
// enabling task-aware models (such as Google Gemini) to optimize vector representations.
type TaskType string

const (
	// TaskUnspecified indicates no specific task hint is provided.
	TaskUnspecified TaskType = ""
	// TaskRetrievalQuery optimizes embeddings for search or retrieval queries.
	TaskRetrievalQuery TaskType = "retrieval_query"
	// TaskRetrievalDocument optimizes embeddings for documents to be indexed and retrieved.
	TaskRetrievalDocument TaskType = "retrieval_document"
	// TaskSemanticSimilarity optimizes embeddings for comparing semantic similarity between texts.
	TaskSemanticSimilarity TaskType = "semantic_similarity"
	// TaskClassification optimizes embeddings for classifying inputs into predefined labels.
	TaskClassification TaskType = "classification"
	// TaskClustering optimizes embeddings for grouping similar inputs into clusters.
	TaskClustering TaskType = "clustering"
)

// Input represents a single item to be embedded. It can hold plain text, multimodal
// content blocks (images, audio, video, documents), or a combination of both.
type Input struct {
	Text  string          `json:"text,omitempty"`
	Parts message.Content `json:"parts,omitempty"`
}

// Text creates an [Input] containing a plain text string.
func Text(text string) Input {
	return Input{
		Text: text,
	}
}

// Texts converts a slice of strings into a slice of text [Input] values.
func Texts(texts ...string) []Input {
	inputs := make([]Input, len(texts))
	for i, t := range texts {
		inputs[i] = Input{Text: t}
	}
	return inputs
}

// Image creates an [Input] containing a single inline image block.
func Image(data []byte, mediaType string) Input {
	return Input{
		Parts: message.Content{
			&message.ImageBlock{
				Data:     data,
				MIMEType: mediaType,
			},
		},
	}
}

// ImageURL creates an [Input] containing a single URL-referenced image block.
func ImageURL(url, mediaType string) Input {
	return Input{
		Parts: message.Content{
			&message.ImageBlock{
				URL:      url,
				MIMEType: mediaType,
			},
		},
	}
}

// Multimodal creates an [Input] from one or more multimodal [message.Block] parts.
func Multimodal(parts ...message.Block) Input {
	return Input{
		Parts: message.Content(parts),
	}
}

// WithParts returns a copy of [Input] with the given multimodal content blocks appended.
func (in Input) WithParts(parts ...message.Block) Input {
	in.Parts = append(in.Parts, parts...)
	return in
}
