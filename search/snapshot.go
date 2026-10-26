package search

import (
	"context"
	"encoding/json"
	"io"
)

// IndexSnapshot represents the serialized state of documents in an index.
type IndexSnapshot struct {
	Version   int         `json:"version"`
	Documents []*Document `json:"documents"`
}

// Snapshot writes all documents in the index to the provided io.Writer in JSON format.
func Snapshot(ctx context.Context, idx *MemoryIndex, w io.Writer) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.closed {
		return ErrIndexClosed
	}

	docs := make([]*Document, 0, len(idx.docs))
	for _, doc := range idx.docs {
		docs = append(docs, doc)
	}

	snapshot := &IndexSnapshot{
		Version:   1,
		Documents: docs,
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

// Restore parses an IndexSnapshot from an io.Reader and re-indexes all documents into the index.
func Restore(ctx context.Context, idx *MemoryIndex, r io.Reader) error {
	if idx.closed {
		return ErrIndexClosed
	}

	var snapshot IndexSnapshot
	if err := json.NewDecoder(r).Decode(&snapshot); err != nil {
		return err
	}

	return idx.IndexBatch(ctx, snapshot.Documents)
}
