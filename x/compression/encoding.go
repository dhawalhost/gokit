package compression

import "strings"

// Encoding represents a supported HTTP content-encoding / compression algorithm.
type Encoding string

const (
	// EncodingZstandard represents Zstandard compression (RFC 8878).
	EncodingZstandard Encoding = "zstd"

	// EncodingBrotli represents Brotli compression (RFC 7932).
	EncodingBrotli Encoding = "br"

	// EncodingGzip represents Gzip compression (RFC 1952).
	EncodingGzip Encoding = "gzip"

	// EncodingDeflate represents Deflate compression (RFC 1951).
	EncodingDeflate Encoding = "deflate"

	// EncodingIdentity represents uncompressed raw byte streams.
	EncodingIdentity Encoding = "identity"
)

// Level controls the compression tradeoff between CPU throughput and byte size reduction.
type Level int

const (
	// LevelDefault uses the codec's balanced default ratio and speed.
	LevelDefault Level = 0

	// LevelBestSpeed prioritizes lowest CPU consumption and latency.
	LevelBestSpeed Level = 1

	// LevelBestCompression prioritizes highest byte reduction ratio at the cost of higher CPU cycles.
	LevelBestCompression Level = 2
)

// String returns the canonical HTTP Content-Encoding token.
func (e Encoding) String() string {
	return string(e)
}

// IsValid checks if the encoding is one of the supported algorithms.
func (e Encoding) IsValid() bool {
	switch e {
	case EncodingZstandard, EncodingBrotli, EncodingGzip, EncodingDeflate, EncodingIdentity:
		return true
	default:
		return false
	}
}

// ParseEncoding converts an incoming string token into a recognized Encoding.
func ParseEncoding(s string) (Encoding, bool) {
	norm := strings.ToLower(strings.TrimSpace(s))
	switch norm {
	case "zstd":
		return EncodingZstandard, true
	case "br":
		return EncodingBrotli, true
	case "gzip":
		return EncodingGzip, true
	case "deflate":
		return EncodingDeflate, true
	case "identity":
		return EncodingIdentity, true
	default:
		return "", false
	}
}
