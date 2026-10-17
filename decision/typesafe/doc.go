// Package loomtypesafe provides a native implementation of the decision.Provider
// interface for TypeSafe AI's System One decision service.
//
// The provider interfaces directly with TypeSafe AI's endpoints:
//   - POST https://api.typesafe.ai/v1/systemone (evaluating probabilistic decisions)
//   - GET https://api.typesafe.ai/v1/models (listing available models)
//
// TypeSafe AI specializes in fast, high-throughput System One reasoning models
// (such as jev-latest, jev-preview, jev-1.14.0, and jev-1.13.0) for predicate,
// categorical choice, and ordinal score evaluations.
package loomtypesafe
