// Package embedding provides text-embedding storage and hybrid
// (substring + semantic) search over a project's message history. It is
// CGO-free: vectors are stored as float32 BLOBs in SQLite and searched
// by brute-force cosine similarity in Go. See
// docs/specs/EMBEDDINGS_AND_VECTOR_SEARCH.md.
package embedding

import (
	"encoding/binary"
	"math"
)

// encodeVector serializes a float32 vector to a little-endian byte slice
// (4 bytes per element) for BLOB storage.
func encodeVector(vec []float32) []byte {
	b := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	return b
}

// decodeVector deserializes a little-endian float32 BLOB back into a
// vector. It returns nil if the byte length is not a multiple of 4.
func decodeVector(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(b)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return vec
}

// cosineSimilarity returns the cosine similarity of two vectors in
// [-1, 1]. Mismatched lengths or a zero-magnitude vector yield 0.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
