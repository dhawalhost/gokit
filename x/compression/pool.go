package compression

import (
	"bytes"
	"io"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// bufferPool recycles scratch bytes.Buffer instances for threshold and buffering checks.
var bufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// acquireBuffer gets a clean bytes.Buffer from the pool.
func acquireBuffer() *bytes.Buffer {
	b := bufferPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

// releaseBuffer returns a bytes.Buffer to the pool.
func releaseBuffer(b *bytes.Buffer) {
	if b == nil {
		return
	}
	if b.Cap() > 64*1024 {
		// Drop oversized buffers to avoid unbounded memory retention
		return
	}
	b.Reset()
	bufferPool.Put(b)
}

// --- GZIP POOLS ---

var (
	gzipWriterPoolDefault = sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
			return w
		},
	}
	gzipWriterPoolBestSpeed = sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
			return w
		},
	}
	gzipWriterPoolBestCompression = sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestCompression)
			return w
		},
	}
	gzipReaderPool = sync.Pool{
		New: func() any {
			return new(gzip.Reader)
		},
	}
)

func acquireGzipWriter(w io.Writer, lvl Level) *gzip.Writer {
	var gz *gzip.Writer
	switch lvl {
	case LevelBestSpeed:
		gz = gzipWriterPoolBestSpeed.Get().(*gzip.Writer)
	case LevelBestCompression:
		gz = gzipWriterPoolBestCompression.Get().(*gzip.Writer)
	default:
		gz = gzipWriterPoolDefault.Get().(*gzip.Writer)
	}
	gz.Reset(w)
	return gz
}

func releaseGzipWriter(gz *gzip.Writer, lvl Level) {
	if gz == nil {
		return
	}
	gz.Reset(io.Discard)
	switch lvl {
	case LevelBestSpeed:
		gzipWriterPoolBestSpeed.Put(gz)
	case LevelBestCompression:
		gzipWriterPoolBestCompression.Put(gz)
	default:
		gzipWriterPoolDefault.Put(gz)
	}
}

func acquireGzipReader(r io.Reader) (*gzip.Reader, error) {
	gz := gzipReaderPool.Get().(*gzip.Reader)
	if err := gz.Reset(r); err != nil {
		gzipReaderPool.Put(gz)
		return nil, err
	}
	return gz, nil
}

func releaseGzipReader(gz *gzip.Reader) {
	if gz == nil {
		return
	}
	_ = gz.Close()
	gzipReaderPool.Put(gz)
}

// --- DEFLATE POOLS ---

var (
	flateWriterPoolDefault = sync.Pool{
		New: func() any {
			w, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
			return w
		},
	}
	flateWriterPoolBestSpeed = sync.Pool{
		New: func() any {
			w, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
			return w
		},
	}
	flateWriterPoolBestCompression = sync.Pool{
		New: func() any {
			w, _ := flate.NewWriter(io.Discard, flate.BestCompression)
			return w
		},
	}
)

func acquireFlateWriter(w io.Writer, lvl Level) *flate.Writer {
	var fw *flate.Writer
	switch lvl {
	case LevelBestSpeed:
		fw = flateWriterPoolBestSpeed.Get().(*flate.Writer)
	case LevelBestCompression:
		fw = flateWriterPoolBestCompression.Get().(*flate.Writer)
	default:
		fw = flateWriterPoolDefault.Get().(*flate.Writer)
	}
	fw.Reset(w)
	return fw
}

func releaseFlateWriter(fw *flate.Writer, lvl Level) {
	if fw == nil {
		return
	}
	fw.Reset(io.Discard)
	switch lvl {
	case LevelBestSpeed:
		flateWriterPoolBestSpeed.Put(fw)
	case LevelBestCompression:
		flateWriterPoolBestCompression.Put(fw)
	default:
		flateWriterPoolDefault.Put(fw)
	}
}

// --- BROTLI POOLS ---

var (
	brotliWriterPoolDefault = sync.Pool{
		New: func() any {
			return brotli.NewWriterLevel(io.Discard, brotli.DefaultCompression)
		},
	}
	brotliWriterPoolBestSpeed = sync.Pool{
		New: func() any {
			return brotli.NewWriterLevel(io.Discard, brotli.BestSpeed)
		},
	}
	brotliWriterPoolBestCompression = sync.Pool{
		New: func() any {
			return brotli.NewWriterLevel(io.Discard, brotli.BestCompression)
		},
	}
	brotliReaderPool = sync.Pool{
		New: func() any {
			return brotli.NewReader(bytes.NewReader(nil))
		},
	}
)

func acquireBrotliWriter(w io.Writer, lvl Level) *brotli.Writer {
	var bw *brotli.Writer
	switch lvl {
	case LevelBestSpeed:
		bw = brotliWriterPoolBestSpeed.Get().(*brotli.Writer)
	case LevelBestCompression:
		bw = brotliWriterPoolBestCompression.Get().(*brotli.Writer)
	default:
		bw = brotliWriterPoolDefault.Get().(*brotli.Writer)
	}
	bw.Reset(w)
	return bw
}

func releaseBrotliWriter(bw *brotli.Writer, lvl Level) {
	if bw == nil {
		return
	}
	bw.Reset(io.Discard)
	switch lvl {
	case LevelBestSpeed:
		brotliWriterPoolBestSpeed.Put(bw)
	case LevelBestCompression:
		brotliWriterPoolBestCompression.Put(bw)
	default:
		brotliWriterPoolDefault.Put(bw)
	}
}

func acquireBrotliReader(r io.Reader) *brotli.Reader {
	br := brotliReaderPool.Get().(*brotli.Reader)
	_ = br.Reset(r)
	return br
}

func releaseBrotliReader(br *brotli.Reader) {
	if br == nil {
		return
	}
	_ = br.Reset(bytes.NewReader(nil))
	brotliReaderPool.Put(br)
}

// --- ZSTD POOLS ---

var (
	zstdEncoderPoolDefault = sync.Pool{
		New: func() any {
			enc, _ := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedDefault))
			return enc
		},
	}
	zstdEncoderPoolBestSpeed = sync.Pool{
		New: func() any {
			enc, _ := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedFastest))
			return enc
		},
	}
	zstdEncoderPoolBestCompression = sync.Pool{
		New: func() any {
			enc, _ := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
			return enc
		},
	}
	zstdDecoderPool = sync.Pool{
		New: func() any {
			dec, _ := zstd.NewReader(bytes.NewReader(nil))
			return dec
		},
	}
)

func acquireZstdEncoder(w io.Writer, lvl Level) *zstd.Encoder {
	var enc *zstd.Encoder
	switch lvl {
	case LevelBestSpeed:
		enc = zstdEncoderPoolBestSpeed.Get().(*zstd.Encoder)
	case LevelBestCompression:
		enc = zstdEncoderPoolBestCompression.Get().(*zstd.Encoder)
	default:
		enc = zstdEncoderPoolDefault.Get().(*zstd.Encoder)
	}
	enc.Reset(w)
	return enc
}

func releaseZstdEncoder(enc *zstd.Encoder, lvl Level) {
	if enc == nil {
		return
	}
	enc.Reset(io.Discard)
	switch lvl {
	case LevelBestSpeed:
		zstdEncoderPoolBestSpeed.Put(enc)
	case LevelBestCompression:
		zstdEncoderPoolBestCompression.Put(enc)
	default:
		zstdEncoderPoolDefault.Put(enc)
	}
}

func acquireZstdDecoder(r io.Reader) (*zstd.Decoder, error) {
	dec := zstdDecoderPool.Get().(*zstd.Decoder)
	if err := dec.Reset(r); err != nil {
		zstdDecoderPool.Put(dec)
		return nil, err
	}
	return dec, nil
}

func releaseZstdDecoder(dec *zstd.Decoder) {
	if dec == nil {
		return
	}
	_ = dec.Reset(bytes.NewReader(nil))
	zstdDecoderPool.Put(dec)
}
