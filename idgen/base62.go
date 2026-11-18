package idgen

import (
	"errors"
	"fmt"
	"math"
)

const (
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base62Radix    = uint64(62)

	leftMask31  = (uint64(1) << 31) - 1
	rightMask32 = (uint64(1) << 32) - 1
)

var (
	// ErrInvalidBase62 is returned when decoding an empty string or invalid Base62 character.
	ErrInvalidBase62 = errors.New("idgen: invalid base62 string")
	// ErrIDOverflow is returned when a decoded Base62 value exceeds math.MaxInt64.
	ErrIDOverflow = errors.New("idgen: base62 value overflows int64")
)

var base62DecodeTable [256]int8

func init() {
	for i := range base62DecodeTable {
		base62DecodeTable[i] = -1
	}
	for i := 0; i < len(base62Alphabet); i++ {
		base62DecodeTable[base62Alphabet[i]] = int8(i)
	}
}

// EncodeBase62 encodes a non-negative 64-bit integer into canonical Base62.
// Negative IDs return an empty string.
func EncodeBase62(id int64) string {
	if id < 0 {
		return ""
	}
	if id == 0 {
		return "0"
	}

	var buf [11]byte
	i := len(buf)
	v := uint64(id)
	for v > 0 {
		i--
		buf[i] = base62Alphabet[v%base62Radix]
		v /= base62Radix
	}
	return string(buf[i:])
}

// DecodeBase62 decodes a canonical Base62 string back into a non-negative int64.
func DecodeBase62(s string) (int64, error) {
	if s == "" {
		return 0, ErrInvalidBase62
	}

	var val uint64
	for i := 0; i < len(s); i++ {
		digit := base62DecodeTable[s[i]]
		if digit < 0 {
			return 0, fmt.Errorf("%w: unexpected byte %q", ErrInvalidBase62, s[i])
		}
		if val > (uint64(math.MaxInt64)-uint64(digit))/base62Radix {
			return 0, ErrIDOverflow
		}
		val = val*base62Radix + uint64(digit)
	}
	return int64(val), nil
}

// ObfuscateID applies a reversible 6-round 63-bit Feistel permutation keyed by secretKey
// and encodes the result in Base62 so sequential IDs become non-enumerable external codes.
// Negative IDs return an empty string.
func ObfuscateID(id int64, secretKey uint64) string {
	if id < 0 {
		return ""
	}

	v := uint64(id)
	left := (v >> 32) & leftMask31
	right := v & rightMask32

	for round := uint64(0); round < 3; round++ {
		left = (left ^ feistelMix(right, secretKey, round*2)) & leftMask31
		right = (right ^ feistelMix(left, secretKey, round*2+1)) & rightMask32
	}

	// #nosec G115 -- left is masked by leftMask31 (31 bits), so combined fits in 63 bits (int64 >= 0)
	permuted := int64((left << 32) | right)
	return EncodeBase62(permuted)
}

// DeobfuscateID decodes a Base62 code produced by ObfuscateID back into the original ID.
func DeobfuscateID(code string, secretKey uint64) (int64, error) {
	permuted, err := DecodeBase62(code)
	if err != nil {
		return 0, err
	}
	if permuted < 0 {
		return 0, fmt.Errorf("idgen: negative permuted value: %d", permuted)
	}

	// #nosec G115 -- permuted is verified non-negative above
	v := uint64(permuted)
	left := (v >> 32) & leftMask31
	right := v & rightMask32

	for round := int64(2); round >= 0; round-- {
		r := uint64(round)
		right = (right ^ feistelMix(left, secretKey, r*2+1)) & rightMask32
		left = (left ^ feistelMix(right, secretKey, r*2)) & leftMask31
	}

	// #nosec G115 -- left is masked by leftMask31 (31 bits), so combined fits in 63 bits (int64 >= 0)
	return int64((left << 32) | right), nil
}

// feistelMix implements a SplitMix64-derived PRF for Feistel round mixing.
func feistelMix(half, secretKey, round uint64) uint64 {
	x := half ^ secretKey ^ (round * 0x9e3779b97f4a7c15)
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
