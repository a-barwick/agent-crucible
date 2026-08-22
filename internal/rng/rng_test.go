package rng

import "testing"

func TestStreamStable(t *testing.T) {
	a := Stream(42, 7)
	b := Stream(42, 7)
	for i := 0; i < 32; i++ {
		if a.Float64() != b.Float64() {
			t.Fatalf("stream diverged at draw %d", i)
		}
	}
}

func TestStreamDivergesAcrossTrials(t *testing.T) {
	a := Stream(42, 0).Float64()
	b := Stream(42, 1).Float64()
	if a == b {
		t.Fatal("adjacent trials produced the same first draw")
	}
}

func TestMixStable(t *testing.T) {
	if Mix(1, 2, 3) != Mix(1, 2, 3) {
		t.Fatal("mix not stable")
	}
	if Mix(1, 2, 3) == Mix(1, 2, 4) {
		t.Fatal("mix ignored last part")
	}
}
