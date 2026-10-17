// Package decision provides a unified System One / Decision Model abstraction for
// fast, structured probabilistic decision-making.
//
// Unlike open-ended generative chat completions, decision models evaluate structured
// questions against an input context (arbitrary state object, string, or multimodal images)
// and output calibrated probabilities and confidence scores.
//
// Three core types of questions are supported:
//   - [QuestionPredicate]: Binary questions evaluating true/false likelihood with confidence.
//   - [QuestionChoice]: Categorical classification selecting among discrete options with
//     per-choice probability distributions.
//   - [QuestionScore]: Ordinal ratings across an ordered scale of descriptive levels with
//     expected score calculation and level probabilities.
//
// Decision models are lightweight, deterministic, and can be composed via middleware
// for telemetry, caching, and evaluation workflows.
package decision
