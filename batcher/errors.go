// Package batcher provides micro-batching and request-coalescing utilities for high-throughput
// concurrent operations, including asynchronous write buffering and DataLoader read batching.
package batcher

import (
	"errors"
)

var (
	// ErrBatcherClosed is returned when attempting to submit work to a closed batcher.
	ErrBatcherClosed = errors.New("batcher: batcher is closed")
	// ErrQueueFull is returned when the submission queue is at capacity and backpressure is set to Reject.
	ErrQueueFull = errors.New("batcher: submission queue is full")
	// ErrKeyNotFound is returned by DataLoader when a requested key is absent from the batch loader's returned map.
	ErrKeyNotFound = errors.New("batcher: key not found in batch result")
	// ErrBatchTimeout is returned when a batch processing operation exceeds its deadline.
	ErrBatchTimeout = errors.New("batcher: batch execution timed out")
)
