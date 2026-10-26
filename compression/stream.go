package compression

import (
	"bytes"
	"io"
	"sync"

	"github.com/klauspost/compress/flate"
)

// NewWriter returns a pooled io.WriteCloser for the given compression algorithm.
// The caller MUST call Close() on the returned writer when finished to flush and return it to the pool.
func NewWriter(codec Encoding, w io.Writer, opts ...Option) (io.WriteCloser, error) {
	if w == nil {
		return nil, ErrNilWriter
	}

	opt := DefaultOptions()
	for _, o := range opts {
		o(&opt)
	}

	switch codec {
	case EncodingGzip:
		gz := acquireGzipWriter(w, opt.Level)
		return &pooledWriteCloser{
			Writer: gz,
			closeFn: func() error {
				err := gz.Close()
				releaseGzipWriter(gz, opt.Level)
				return err
			},
		}, nil

	case EncodingDeflate:
		fw := acquireFlateWriter(w, opt.Level)
		return &pooledWriteCloser{
			Writer: fw,
			closeFn: func() error {
				err := fw.Close()
				releaseFlateWriter(fw, opt.Level)
				return err
			},
		}, nil

	case EncodingBrotli:
		bw := acquireBrotliWriter(w, opt.Level)
		return &pooledWriteCloser{
			Writer: bw,
			closeFn: func() error {
				err := bw.Close()
				releaseBrotliWriter(bw, opt.Level)
				return err
			},
		}, nil

	case EncodingZstandard:
		enc := acquireZstdEncoder(w, opt.Level)
		return &pooledWriteCloser{
			Writer: enc,
			closeFn: func() error {
				err := enc.Close()
				releaseZstdEncoder(enc, opt.Level)
				return err
			},
		}, nil

	case EncodingIdentity:
		return &nopWriteCloser{Writer: w}, nil

	default:
		return nil, ErrUnsupportedEncoding
	}
}

// NewReader returns a pooled io.ReadCloser that decompresses data from r.
// The caller MUST call Close() when finished reading to recycle internal buffers.
func NewReader(codec Encoding, r io.Reader) (io.ReadCloser, error) {
	if r == nil {
		return nil, ErrNilReader
	}

	switch codec {
	case EncodingGzip:
		gz, err := acquireGzipReader(r)
		if err != nil {
			return nil, ErrCorruptedData
		}
		return &pooledReadCloser{
			Reader: gz,
			closeFn: func() error {
				err := gz.Close()
				releaseGzipReader(gz)
				return err
			},
		}, nil

	case EncodingDeflate:
		fr := flate.NewReader(r)
		return fr, nil

	case EncodingBrotli:
		br := acquireBrotliReader(r)
		return &pooledReadCloser{
			Reader: br,
			closeFn: func() error {
				releaseBrotliReader(br)
				return nil
			},
		}, nil

	case EncodingZstandard:
		dec, err := acquireZstdDecoder(r)
		if err != nil {
			return nil, ErrCorruptedData
		}
		return &pooledReadCloser{
			Reader: dec,
			closeFn: func() error {
				releaseZstdDecoder(dec)
				return nil
			},
		}, nil

	case EncodingIdentity:
		return io.NopCloser(r), nil

	default:
		return nil, ErrUnsupportedEncoding
	}
}

// Compress streams uncompressed bytes from r, compresses them with codec, and writes to w.
func Compress(codec Encoding, r io.Reader, w io.Writer, opts ...Option) error {
	if r == nil {
		return ErrNilReader
	}
	if w == nil {
		return ErrNilWriter
	}

	wc, err := NewWriter(codec, w, opts...)
	if err != nil {
		return err
	}

	if _, err = io.Copy(wc, r); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

// Decompress reads compressed bytes from r, decompresses them with codec, and writes uncompressed bytes to w.
func Decompress(codec Encoding, r io.Reader, w io.Writer) error {
	if r == nil {
		return ErrNilReader
	}
	if w == nil {
		return ErrNilWriter
	}

	rc, err := NewReader(codec, r)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	if _, err := io.Copy(w, rc); err != nil {
		return ErrCorruptedData
	}
	return nil
}

// CompressBytes compresses a raw byte slice using the requested algorithm.
func CompressBytes(codec Encoding, data []byte, opts ...Option) ([]byte, error) {
	buf := acquireBuffer()
	defer releaseBuffer(buf)

	if err := Compress(codec, bytes.NewReader(data), buf, opts...); err != nil {
		return nil, err
	}

	// Allocate exact destination slice
	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res, nil
}

// DecompressBytes decompresses a compressed byte slice using the requested algorithm.
func DecompressBytes(codec Encoding, data []byte) ([]byte, error) {
	buf := acquireBuffer()
	defer releaseBuffer(buf)

	if err := Decompress(codec, bytes.NewReader(data), buf); err != nil {
		return nil, err
	}

	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res, nil
}

// pooledWriteCloser coordinates pooled writer lifecycle.
type pooledWriteCloser struct {
	io.Writer
	closeFn func() error
	once    sync.Once
	closed  bool
}

func (p *pooledWriteCloser) Close() error {
	var err error
	p.once.Do(func() {
		p.closed = true
		if p.closeFn != nil {
			err = p.closeFn()
		}
	})
	return err
}

// pooledReadCloser coordinates pooled reader lifecycle.
type pooledReadCloser struct {
	io.Reader
	closeFn func() error
	once    sync.Once
	closed  bool
}

func (p *pooledReadCloser) Close() error {
	var err error
	p.once.Do(func() {
		p.closed = true
		if p.closeFn != nil {
			err = p.closeFn()
		}
	})
	return err
}

type nopWriteCloser struct {
	io.Writer
}

func (n *nopWriteCloser) Close() error {
	return nil
}
