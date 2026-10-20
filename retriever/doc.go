// Package retriever defines unified abstractions, middleware, and algorithms for document
// retrieval and reranking in RAG workflows.
//
// Key components include:
//   - [Retriever]: Core interface for retrieving documents relevant to a query.
//   - [VectorStoreRetriever]: Adapts any [vectorstore.VectorStore] to the [Retriever] interface with telemetry.
//   - [MergerRetriever]: Fuses results from multiple retrievers using Reciprocal Rank Fusion (RRF).
//   - [DecisionReranker]: Evaluates document relevance using probabilistic decision models.
//   - [NewTool]: Adapts any [Retriever] into an executable [tool.Tool] callable by LLMs.
package retriever
