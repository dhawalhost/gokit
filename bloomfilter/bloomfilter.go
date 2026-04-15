// Package bloomfilter provides a space-efficient probabilistic data structure
// for testing set membership. False positives are possible; false negatives
// are not. Two backends are provided:
//
//   - [Filter]     - pure in-memory implementation (single process).
//   - [RedisStore] - Redis-backed implementation for distributed/shared use.
package bloomfilter

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/bits"
)

// Sentinel errors returned by this package.
var (
	ErrExpectedItemsZero        = errors.New("bloomfilter: expectedItems must be > 0")
	ErrInvalidFalsePositiveRate = errors.New("bloomfilter: falsePositiveRate must be in (0, 1)")
	ErrNilRedisClient           = errors.New("bloomfilter: redis client must not be nil")
	ErrEmptyKey                 = errors.New("bloomfilter: key must not be empty")
	ErrDataTooShort             = errors.New("bloomfilter: data too short")
)

// Filter is a thread-unsafe in-memory Bloom filter.
// Use a sync.Mutex or sync.RWMutex when sharing across goroutines.
type Filter struct {
	bitset []uint64
	m      uint
	k      uint
	count  uint
}

// New creates a Filter sized for expectedItems distinct elements at the
// requested falsePositiveRate (0 < p < 1).
func New(expectedItems uint, falsePositiveRate float64) (*Filter, error) {
	if expectedItems == 0 {
		return nil, ErrExpectedItemsZero
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return nil, ErrInvalidFalsePositiveRate
	}
	m := optimalBitSize(expectedItems, falsePositiveRate)
	k := optimalHashCount(m, expectedItems)
	words := (m + 63) / 64
	return &Filter{
		bitset: make([]uint64, words),
		m:      m,
		k:      k,
	}, nil
}

// Add inserts data into the filter.
func (f *Filter) Add(data []byte) {
	h1, h2 := hashes(data)
	for i := uint(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(f.m)
		f.bitset[idx/64] |= 1 << (idx % 64)
	}
	f.count++
}

// AddString inserts a string into the filter.
func (f *Filter) AddString(s string) { f.Add([]byte(s)) }

// Contains reports whether data is possibly in the filter.
func (f *Filter) Contains(data []byte) bool {
	h1, h2 := hashes(data)
	for i := uint(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(f.m)
		if f.bitset[idx/64]&(1<<(idx%64)) == 0 {
			return false
		}
	}
	return true
}

// ContainsString reports whether the string is possibly in the filter.
func (f *Filter) ContainsString(s string) bool { return f.Contains([]byte(s)) }

// Reset clears all bits and resets the element count.
func (f *Filter) Reset() {
	for i := range f.bitset {
		f.bitset[i] = 0
	}
	f.count = 0
}

// Count returns the number of Add calls since creation or last Reset.
func (f *Filter) Count() uint { return f.count }

// BitSize returns the total number of bits allocated for this filter.
func (f *Filter) BitSize() uint { return f.m }

// HashFunctions returns the number of hash functions (k) used.
func (f *Filter) HashFunctions() uint { return f.k }

// EstimatedFalsePositiveRate returns the current theoretical false-positive
// probability: p = (1 - exp(-k*n/m))^k
func (f *Filter) EstimatedFalsePositiveRate() float64 {
	if f.m == 0 || f.count == 0 {
		return 0
	}
	exponent := -float64(f.k) * float64(f.count) / float64(f.m)
	return math.Pow(1-math.Exp(exponent), float64(f.k))
}

// OnesCount returns the number of set bits; useful for diagnostics.
func (f *Filter) OnesCount() uint {
	var n uint
	for _, w := range f.bitset {
		n += safeUintFromInt(bits.OnesCount64(w))
	}
	return n
}

// MarshalBinary encodes the filter for persistence or transfer.
// Wire format (little-endian): m(8) k(8) count(8) bits(8*words).
func (f *Filter) MarshalBinary() ([]byte, error) {
	words := len(f.bitset)
	buf := make([]byte, 24+8*words)
	binary.LittleEndian.PutUint64(buf[0:], uint64(f.m))
	binary.LittleEndian.PutUint64(buf[8:], uint64(f.k))
	binary.LittleEndian.PutUint64(buf[16:], uint64(f.count))
	for i, w := range f.bitset {
		binary.LittleEndian.PutUint64(buf[24+8*i:], w)
	}
	return buf, nil
}

// UnmarshalBinary restores a filter from data produced by MarshalBinary.
func (f *Filter) UnmarshalBinary(data []byte) error {
	if len(data) < 24 {
		return fmt.Errorf("%w: got %d bytes, need at least 24", ErrDataTooShort, len(data))
	}
	m := binary.LittleEndian.Uint64(data[0:])
	k := binary.LittleEndian.Uint64(data[8:])
	count := binary.LittleEndian.Uint64(data[16:])
	words := (m + 63) / 64
	if uint64(len(data)) < 24+8*words {
		return fmt.Errorf("%w: need %d bits worth of data", ErrDataTooShort, m)
	}
	bitset := make([]uint64, words)
	for i := range bitset {
		bitset[i] = binary.LittleEndian.Uint64(data[24+8*uint64(i):])
	}
	f.m = uint(m)
	f.k = uint(k)
	f.count = uint(count)
	f.bitset = bitset
	return nil
}

func optimalBitSize(n uint, p float64) uint {
	m := -float64(n) * math.Log(p) / (math.Log(2) * math.Log(2))
	return uint(math.Ceil(m))
}

func optimalHashCount(m, n uint) uint {
	k := float64(m) / float64(n) * math.Log(2)
	h := uint(math.Round(k))
	if h < 1 {
		h = 1
	}
	return h
}

// hashes produces two independent 64-bit values via FNV-1a and FNV-1.
// h2 is forced odd so every bit position is reachable via double-hashing.
func hashes(data []byte) (uint64, uint64) {
	ha := fnv.New64a()
	_, _ = io.Writer(ha).Write(data)
	v1 := ha.Sum64()

	hb := fnv.New64()
	_, _ = io.Writer(hb).Write(data)
	v2 := hb.Sum64()
	if v2%2 == 0 {
		v2++
	}
	return v1, v2
}

func safeUintFromInt(v int) uint {
	if v < 0 {
		return 0
	}
	return uint(v)
}
