package bloomfilter_test

import (
	"testing"

	"github.com/dhawalhost/gokit/bloomfilter"
)

func TestNewRejectsZeroItems(t *testing.T) {
	_, err := bloomfilter.New(0, 0.01)
	if err == nil {
		t.Fatal("expected error for expectedItems=0")
	}
}

func TestNewRejectsInvalidRate(t *testing.T) {
	for _, p := range []float64{0, -0.1, 1, 1.5} {
		_, err := bloomfilter.New(1000, p)
		if err == nil {
			t.Fatalf("expected error for falsePositiveRate=%v", p)
		}
	}
}

func TestNewSetsMetadata(t *testing.T) {
	f, err := bloomfilter.New(1000, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f.BitSize() == 0 {
		t.Error("BitSize should be > 0")
	}
	if f.HashFunctions() == 0 {
		t.Error("HashFunctions should be > 0")
	}
}

func TestContainsAfterAdd(t *testing.T) {
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
}

func TestDefinitelyAbsent(t *testing.T) {
	f, _ := bloomfilter.New(1000, 0.001)
	f.AddString("hello")
	if f.ContainsString("definitely-not-added-xyzzy-42") {
		t.Error("unexpected false positive for unseen item")
	}
}

func TestCountIncrementsOnAdd(t *testing.T) {
	f, _ := bloomfilter.New(100, 0.01)
	for i := 0; i < 5; i++ {
		f.AddString("x")
	}
	if f.Count() != 5 {
		t.Errorf("Count = %d, want 5", f.Count())
	}
}

func TestResetClearsFilter(t *testing.T) {
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
}

func TestFalsePositiveRateZeroBeforeInserts(t *testing.T) {
	f, _ := bloomfilter.New(1000, 0.01)
	if r := f.EstimatedFalsePositiveRate(); r != 0 {
		t.Errorf("EstimatedFPR on empty filter = %f, want 0", r)
	}
}

func TestFalsePositiveRateIncreases(t *testing.T) {
	f, _ := bloomfilter.New(10, 0.1)
	prev := f.EstimatedFalsePositiveRate()
	for i := 0; i < 10; i++ {
		f.Add([]byte{byte(i)})
		cur := f.EstimatedFalsePositiveRate()
		if cur < prev {
			t.Errorf("FPR decreased from %f to %f after insert %d", prev, cur, i)
		}
		prev = cur
	}
}

func TestMarshalRoundtrip(t *testing.T) {
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
}

func TestUnmarshalRejectsTooShort(t *testing.T) {
	var f bloomfilter.Filter
	if err := f.UnmarshalBinary([]byte{0, 1, 2}); err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestAddRawBytes(t *testing.T) {
	f, _ := bloomfilter.New(100, 0.01)
	f.Add([]byte{0x00, 0xFF, 0xAB})
	if !f.Contains([]byte{0x00, 0xFF, 0xAB}) {
		t.Error("raw bytes not found after Add")
	}
}
