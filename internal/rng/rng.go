// Package rng builds deterministic random streams from (seed, trial).
// The same pair always yields the same sequence of draws.
package rng

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand"
)

// Stream returns a dedicated *rand.Rand for one trial.
// Mixing goes through SHA-256 so nearby seeds do not produce correlated streams.
func Stream(seed int64, trial int) *rand.Rand {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(seed))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(trial))
	sum := sha256.Sum256(buf[:])
	src := int64(binary.LittleEndian.Uint64(sum[:8]))
	return rand.New(rand.NewSource(src))
}

// Mix folds extra integers into a derived seed. Useful for sub-streams
// that must stay stable even if the parent stream is consumed differently.
func Mix(seed int64, parts ...int64) int64 {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	_, _ = h.Write(b[:])
	for _, p := range parts {
		binary.LittleEndian.PutUint64(b[:], uint64(p))
		_, _ = h.Write(b[:])
	}
	sum := h.Sum(nil)
	return int64(binary.LittleEndian.Uint64(sum[:8]))
}

// Sub returns an independent stream for one named decision site, keyed by
// the site's label and how many times that site has already been visited.
//
// This is what keeps the ensemble stable: a site's draws depend only on its
// own key and visit count, so a run that takes a different path elsewhere —
// one extra retry, one skipped node — cannot shift the values every other
// site sees.
func Sub(seed int64, key string, n int) *rand.Rand {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(key))
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
	sum := h.Sum(nil)
	return rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(sum[:8]))))
}
