package compression

// DefaultMinLength is the minimum byte size threshold (1KB) below which responses are not compressed.
const DefaultMinLength = 1024

// DefaultMIMETypes are common compressible content types.
var DefaultMIMETypes = []string{
	"text/html",
	"text/css",
	"text/plain",
	"text/javascript",
	"text/xml",
	"application/json",
	"application/javascript",
	"application/xml",
	"application/x-yaml",
	"application/wasm",
	"application/graphql",
	"image/svg+xml",
	"application/vnd.api+json",
}

// DefaultEncodings is the server preference order when client supports multiple algorithms.
var DefaultEncodings = []Encoding{
	EncodingZstandard,
	EncodingBrotli,
	EncodingGzip,
	EncodingDeflate,
}

// Options configures compression behavior for streaming and HTTP middleware.
type Options struct {
	Level               Level
	MinLength           int
	Encodings           []Encoding
	MIMETypes           []string
	EnableDecompression bool
}

// Option configures an Options struct.
type Option func(*Options)

// DefaultOptions returns production-ready default compression options.
func DefaultOptions() Options {
	return Options{
		Level:               LevelDefault,
		MinLength:           DefaultMinLength,
		Encodings:           DefaultEncodings,
		MIMETypes:           DefaultMIMETypes,
		EnableDecompression: true,
	}
}

// WithLevel sets the compression tradeoff level (LevelDefault, LevelBestSpeed, LevelBestCompression).
func WithLevel(lvl Level) Option {
	return func(o *Options) {
		o.Level = lvl
	}
}

// WithMinLength sets the minimum response byte length required to trigger compression.
func WithMinLength(minLen int) Option {
	return func(o *Options) {
		if minLen >= 0 {
			o.MinLength = minLen
		}
	}
}

// WithEncodings configures allowed compression algorithms in order of server preference.
func WithEncodings(encs ...Encoding) Option {
	return func(o *Options) {
		if len(encs) > 0 {
			valid := make([]Encoding, 0, len(encs))
			for _, e := range encs {
				if e.IsValid() && e != EncodingIdentity {
					valid = append(valid, e)
				}
			}
			if len(valid) > 0 {
				o.Encodings = valid
			}
		}
	}
}

// WithMIMETypes specifies which Content-Type prefixes should be compressed.
func WithMIMETypes(mimes ...string) Option {
	return func(o *Options) {
		if len(mimes) > 0 {
			o.MIMETypes = mimes
		}
	}
}

// WithDecompression configures whether incoming compressed request bodies are automatically decompressed.
func WithDecompression(enabled bool) Option {
	return func(o *Options) {
		o.EnableDecompression = enabled
	}
}
