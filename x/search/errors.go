package search

import "errors"

var (
	// ErrDocumentNotFound is returned when attempting to retrieve or delete a non-existent document.
	ErrDocumentNotFound = errors.New("search: document not found")

	// ErrEmptyDocumentID is returned when indexing a document with an empty ID string.
	ErrEmptyDocumentID = errors.New("search: document ID cannot be empty")

	// ErrIndexClosed is returned when operations are attempted on a closed index.
	ErrIndexClosed = errors.New("search: index is closed")

	// ErrInvalidQuery is returned when a query is nil, malformed, or has invalid clauses.
	ErrInvalidQuery = errors.New("search: invalid search query")

	// ErrInvalidField is returned when searching or boosting a field that is unindexed.
	ErrInvalidField = errors.New("search: invalid or empty field name")
)
