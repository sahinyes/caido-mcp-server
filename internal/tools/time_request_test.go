package tools

import "testing"

func TestComputeTimingSummary_OddCount(t *testing.T) {
	ns := []int64{100, 300, 200, 500, 400}
	s := computeTimingSummary(ns)

	if s.MinNs != 100 {
		t.Fatalf("min: want 100, got %d", s.MinNs)
	}
	if s.MaxNs != 500 {
		t.Fatalf("max: want 500, got %d", s.MaxNs)
	}
	if s.MedianNs != 300 {
		t.Fatalf("median: want 300, got %d", s.MedianNs)
	}
	if s.Count != 5 {
		t.Fatalf("count: want 5, got %d", s.Count)
	}
}

func TestComputeTimingSummary_EvenCount(t *testing.T) {
	ns := []int64{100, 200, 300, 400}
	s := computeTimingSummary(ns)

	if s.MedianNs != 250 {
		t.Fatalf("median: want 250, got %d", s.MedianNs)
	}
	if s.MinNs != 100 {
		t.Fatalf("min: want 100, got %d", s.MinNs)
	}
	if s.MaxNs != 400 {
		t.Fatalf("max: want 400, got %d", s.MaxNs)
	}
}

func TestComputeTimingSummary_SingleSample(t *testing.T) {
	ns := []int64{999_000_000}
	s := computeTimingSummary(ns)

	if s.MinNs != 999_000_000 {
		t.Fatalf("min: want 999000000, got %d", s.MinNs)
	}
	if s.MedianNs != 999_000_000 {
		t.Fatalf("median: want 999000000, got %d", s.MedianNs)
	}
	if s.P95Ns != 999_000_000 {
		t.Fatalf("p95: want 999000000, got %d", s.P95Ns)
	}
	if s.MaxNs != 999_000_000 {
		t.Fatalf("max: want 999000000, got %d", s.MaxNs)
	}
	if s.Count != 1 {
		t.Fatalf("count: want 1, got %d", s.Count)
	}
}

func TestComputeTimingSummary_P95(t *testing.T) {
	// 20 sample: 19x 100ns, 1x 5000ns (outlier)
	ns := make([]int64, 20)
	for i := range ns {
		ns[i] = 100
	}
	ns[19] = 5000

	s := computeTimingSummary(ns)

	if s.MinNs != 100 {
		t.Fatalf("min: want 100, got %d", s.MinNs)
	}
	if s.MaxNs != 5000 {
		t.Fatalf("max: want 5000, got %d", s.MaxNs)
	}
	// p95 = index 19 (0.95*20=19) → 5000
	if s.P95Ns != 5000 {
		t.Fatalf("p95: want 5000, got %d", s.P95Ns)
	}
	if s.MedianNs != 100 {
		t.Fatalf("median: want 100, got %d", s.MedianNs)
	}
}
