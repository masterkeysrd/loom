package embedding

import (
	"encoding/binary"
	"math"
)

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

// Bytes packs the vector into little-endian bytes (4 bytes per float32 element).
func (v Vector) Bytes() []byte {
	return EncodeVector(v)
}

// EncodeVector packs a [Vector] into little-endian bytes (4 bytes per element).
func EncodeVector(v Vector) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// DecodeVector unpacks a little-endian byte slice into a [Vector].
func DecodeVector(b []byte) Vector {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	n := len(b) / 4
	out := make(Vector, n)
	for i := range n {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
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

// CosineSimilarityBytes computes the cosine similarity between two packed little-endian
// float32 byte slices using 4-way loop unrolling. Returns -1 if slices are empty,
// have different lengths, or are not a multiple of 4 bytes.
func CosineSimilarityBytes(a, b []byte) float64 {
	if len(a) == 0 || len(a) != len(b) || len(a)%4 != 0 {
		return -1
	}
	n := len(a)

	var dot0, dot1, dot2, dot3 float32
	var na0, na1, na2, na3 float32
	var nb0, nb1, nb2, nb3 float32

	i := 0
	for ; i <= n-16; i += 16 {
		a0 := math.Float32frombits(binary.LittleEndian.Uint32(a[i : i+4]))
		a1 := math.Float32frombits(binary.LittleEndian.Uint32(a[i+4 : i+8]))
		a2 := math.Float32frombits(binary.LittleEndian.Uint32(a[i+8 : i+12]))
		a3 := math.Float32frombits(binary.LittleEndian.Uint32(a[i+12 : i+16]))

		b0 := math.Float32frombits(binary.LittleEndian.Uint32(b[i : i+4]))
		b1 := math.Float32frombits(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		b2 := math.Float32frombits(binary.LittleEndian.Uint32(b[i+8 : i+12]))
		b3 := math.Float32frombits(binary.LittleEndian.Uint32(b[i+12 : i+16]))

		dot0 += a0 * b0
		dot1 += a1 * b1
		dot2 += a2 * b2
		dot3 += a3 * b3

		na0 += a0 * a0
		na1 += a1 * a1
		na2 += a2 * a2
		na3 += a3 * a3

		nb0 += b0 * b0
		nb1 += b1 * b1
		nb2 += b2 * b2
		nb3 += b3 * b3
	}
	dot := dot0 + dot1 + dot2 + dot3
	normA := na0 + na1 + na2 + na3
	normB := nb0 + nb1 + nb2 + nb3
	for ; i < n; i += 4 {
		av := math.Float32frombits(binary.LittleEndian.Uint32(a[i : i+4]))
		bv := math.Float32frombits(binary.LittleEndian.Uint32(b[i : i+4]))
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float64(dot / float32(math.Sqrt(float64(normA)*float64(normB))))
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
