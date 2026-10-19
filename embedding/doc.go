// Package embedding provides a unified abstraction for generating dense vector
// embeddings across text and multimodal inputs.
//
// An [embedding.Model] wraps a concrete [Provider] (such as Ollama, OpenAI, or
// Google GenAI) and supports:
//   - Batch and single-input embedding generation ([Model.Embed], [Model.EmbedInput])
//   - Task-aware convenience helpers ([Model.EmbedQuery], [Model.EmbedDocuments])
//   - Multimodal inputs combining text, images, audio, video, and documents ([Input])
//   - Vector similarity and distance operations ([Vector.Dot], [Vector.CosineSimilarity],
//     [Vector.EuclideanDistance], [Vector.Normalize])
//   - Composable middleware for OpenTelemetry tracing, metrics, and cost estimation
package embedding
