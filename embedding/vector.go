package embedding

import "math"

// Vector represents a dense floating-point embedding vector.
type Vector []float32

// Dimension returns the number of elements in the vector.
func (v Vector) Dimension() int {
	return len(v)
}

// Float64 converts the vector into a slice of float64 values.
func (v Vector) Float64() []float64 {
	if v == nil {
		return nil
	}
	out := make([]float64, len(v))
	for i, val := range v {
		out[i] = float64(val)
	}
	return out
}

// Dot computes the dot product between v and other.
// If the vectors have different lengths or are empty, it returns 0.
func (v Vector) Dot(other Vector) float32 {
	if len(v) == 0 || len(v) != len(other) {
		return 0
	}
	var sum float64
	for i := range v {
		sum += float64(v[i]) * float64(other[i])
	}
	return float32(sum)
}

// CosineSimilarity computes the cosine similarity between v and other in [-1, 1].
// If the vectors have different lengths, are empty, or either vector has zero magnitude,
// it returns 0.
func (v Vector) CosineSimilarity(other Vector) float32 {
	if len(v) == 0 || len(v) != len(other) {
		return 0
	}
	var dot, normA, normB float64
	for i := range v {
		a := float64(v[i])
		b := float64(other[i])
		dot += a * b
		normA += a * a
		normB += b * b
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// EuclideanDistance computes the L2 (Euclidean) distance between v and other.
// If the vectors have different lengths or are empty, it returns 0.
func (v Vector) EuclideanDistance(other Vector) float32 {
	if len(v) == 0 || len(v) != len(other) {
		return 0
	}
	var sumSq float64
	for i := range v {
		diff := float64(v[i]) - float64(other[i])
		sumSq += diff * diff
	}
	return float32(math.Sqrt(sumSq))
}

// Normalize returns a new unit-length Vector (L2 norm = 1).
// If v is empty, it returns nil. If v has zero magnitude, it returns a copy of v.
func (v Vector) Normalize() Vector {
	if len(v) == 0 {
		return nil
	}
	var sumSq float64
	for _, val := range v {
		f := float64(val)
		sumSq += f * f
	}
	norm := math.Sqrt(sumSq)
	out := make(Vector, len(v))
	if norm == 0 {
		copy(out, v)
		return out
	}
	for i, val := range v {
		out[i] = float32(float64(val) / norm)
	}
	return out
}
