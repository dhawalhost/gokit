package bloomfilter_test

import (
	"fmt"
	"testing"

	"github.com/dhawalhost/gokit/bloomfilter"
)

func BenchmarkAdd(b *testing.B) {
	f, _ := bloomfilter.New(uint(b.N)+1, 0.01)
	data := []byte("benchmark-key")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Add(data)
	}
}

func BenchmarkAddString(b *testing.B) {
	f, _ := bloomfilter.New(uint(b.N)+1, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.AddString("benchmark-key")
	}
}

func BenchmarkContainsHit(b *testing.B) {
	f, _ := bloomfilter.New(1, 0.01)
	f.AddString("hit")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.ContainsString("hit")
	}
}

func BenchmarkContainsMiss(b *testing.B) {
	f, _ := bloomfilter.New(1, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.ContainsString("miss")
	}
}

func BenchmarkMixed(b *testing.B) {
	f, _ := bloomfilter.New(uint(b.N)+1, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		f.AddString(key)
		f.ContainsString(key)
	}
}

func BenchmarkScaling(b *testing.B) {
	sizes := []struct {
		n uint
		p float64
	}{
		{1_000, 0.01},
		{10_000, 0.01},
		{100_000, 0.01},
		{100_000, 0.001},
	}
	for _, tc := range sizes {
		b.Run(fmt.Sprintf("n=%d/p=%.3f", tc.n, tc.p), func(b *testing.B) {
			f, _ := bloomfilter.New(tc.n, tc.p)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f.Add([]byte(fmt.Sprintf("k%d", i)))
			}
		})
	}
}

func BenchmarkAddAllocs(b *testing.B) {
	f, _ := bloomfilter.New(uint(b.N)+1, 0.01)
	data := []byte("alloc-test")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Add(data)
	}
}

func BenchmarkContainsAllocs(b *testing.B) {
	f, _ := bloomfilter.New(1000, 0.01)
	f.AddString("probe")
	data := []byte("probe")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Contains(data)
	}
}
