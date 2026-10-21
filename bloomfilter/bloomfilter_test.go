package bloomfilter_test

import (
	"testing"

	"github.com/dhawalhost/gokit/bloomfilter"
)

func TestBloomFilter_CreationAndValidation_TableDriven(t *testing.T) {
	tests := []struct {
		name          string
		expectedItems uint
		fpRate        float64
		wantErr       bool
	}{
		{
			name:          "rejects zero items",
			expectedItems: 0,
			fpRate:        0.01,
			wantErr:       true,
		},
		{
			name:          "rejects rate of zero",
			expectedItems: 1000,
			fpRate:        0,
			wantErr:       true,
		},
		{
			name:          "rejects negative rate",
			expectedItems: 1000,
			fpRate:        -0.1,
			wantErr:       true,
		},
		{
			name:          "rejects rate of one",
			expectedItems: 1000,
			fpRate:        1.0,
			wantErr:       true,
		},
		{
			name:          "rejects rate greater than one",
			expectedItems: 1000,
			fpRate:        1.5,
			wantErr:       true,
		},
		{
			name:          "valid configuration sets positive bit size and hash function count",
			expectedItems: 1000,
			fpRate:        0.01,
			wantErr:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := bloomfilter.New(tc.expectedItems, tc.fpRate)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr {
				if f.BitSize() == 0 {
					t.Error("BitSize should be > 0")
				}
				if f.HashFunctions() == 0 {
					t.Error("HashFunctions should be > 0")
				}
			}
		})
	}
}

func TestBloomFilter_Operations_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "added string items and raw bytes are contained in filter",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(100, 0.01)
				items := []string{"apple", "banana", "cherry", "durian", "elderberry"}
				for _, s := range items {
					f.AddString(s)
				}
				for _, s := range items {
					if !f.ContainsString(s) {
						t.Errorf("expected %q to be in filter after Add", s)
					}
				}

				raw := []byte{0x00, 0xFF, 0xAB}
				f.Add(raw)
				if !f.Contains(raw) {
					t.Error("raw bytes not found after Add")
				}
			},
		},
		{
			name: "unseen items are reported as definitely absent",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(1000, 0.001)
				f.AddString("hello")
				if f.ContainsString("definitely-not-added-xyzzy-42") {
					t.Error("unexpected false positive for unseen item")
				}
			},
		},
		{
			name: "count increments on every insertion",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(100, 0.01)
				for i := 0; i < 5; i++ {
					f.AddString("x")
				}
				if f.Count() != 5 {
					t.Errorf("Count = %d, want 5", f.Count())
				}
			},
		},
		{
			name: "reset clears bits and resets count to zero",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(100, 0.01)
				f.AddString("gone")
				f.Reset()
				if f.ContainsString("gone") {
					t.Error("item should not be found after Reset")
				}
				if f.Count() != 0 {
					t.Errorf("Count after Reset = %d, want 0", f.Count())
				}
				if f.OnesCount() != 0 {
					t.Errorf("OnesCount after Reset = %d, want 0", f.OnesCount())
				}
			},
		},
		{
			name: "estimated false positive rate tracks insertions accurately",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(10, 0.1)
				if r := f.EstimatedFalsePositiveRate(); r != 0 {
					t.Errorf("EstimatedFPR on empty filter = %f, want 0", r)
				}
				prev := f.EstimatedFalsePositiveRate()
				for i := 0; i < 10; i++ {
					f.Add([]byte{byte(i)})
					cur := f.EstimatedFalsePositiveRate()
					if cur < prev {
						t.Errorf("FPR decreased from %f to %f after insert %d", prev, cur, i)
					}
					prev = cur
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestBloomFilter_Serialization_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "binary roundtrip preserves filter bits, metadata and elements",
			run: func(t *testing.T) {
				f, _ := bloomfilter.New(500, 0.01)
				for _, s := range []string{"one", "two", "three"} {
					f.AddString(s)
				}
				data, err := f.MarshalBinary()
				if err != nil {
					t.Fatalf("MarshalBinary: %v", err)
				}
				var f2 bloomfilter.Filter
				if err := f2.UnmarshalBinary(data); err != nil {
					t.Fatalf("UnmarshalBinary: %v", err)
				}
				for _, s := range []string{"one", "two", "three"} {
					if !f2.ContainsString(s) {
						t.Errorf("restored filter missing %q", s)
					}
				}
				if f2.Count() != f.Count() {
					t.Errorf("Count mismatch: got %d, want %d", f2.Count(), f.Count())
				}
				if f2.BitSize() != f.BitSize() {
					t.Errorf("BitSize mismatch: got %d, want %d", f2.BitSize(), f.BitSize())
				}
			},
		},
		{
			name: "unmarshal rejects truncated binary payload",
			run: func(t *testing.T) {
				var f bloomfilter.Filter
				if err := f.UnmarshalBinary([]byte{0, 1, 2}); err == nil {
					t.Fatal("expected error for short data")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
