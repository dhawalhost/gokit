package compression

import "errors"

var (
	// ErrUnsupportedEncoding is returned when an unhandled or invalid compression algorithm is specified.
	ErrUnsupportedEncoding = errors.New("compression: unsupported encoding algorithm")

	// ErrCorruptedData is returned when decompressing malformed or truncated compressed payload.
	ErrCorruptedData = errors.New("compression: corrupted or invalid compressed data")

	// ErrNilReader is returned when a nil io.Reader is supplied to compression or decompression functions.
	ErrNilReader = errors.New("compression: reader cannot be nil")

	// ErrNilWriter is returned when a nil io.Writer is supplied to compression or decompression functions.
	ErrNilWriter = errors.New("compression: writer cannot be nil")

	// ErrInvalidLevel is returned when an out-of-range compression level is configured.
	ErrInvalidLevel = errors.New("compression: invalid compression level")
)
