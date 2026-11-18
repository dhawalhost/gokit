// Package gateway provides API Gateway and Backend-For-Frontend (BFF) building blocks,
// including scatter-gather parallel fanout aggregation, reverse proxy routing, and load balancing.
package gateway

import (
	"errors"
)

var (
	// ErrNoHealthyTargets is returned when all backend targets for a route fail health checks.
	ErrNoHealthyTargets = errors.New("gateway: no healthy targets available")
	// ErrCriticalTaskFailed is returned when a critical task in an aggregation group fails.
	ErrCriticalTaskFailed = errors.New("gateway: critical aggregation task failed")
	// ErrTaskTimeout is returned when an aggregation task exceeds its execution deadline.
	ErrTaskTimeout = errors.New("gateway: aggregation task execution timed out")
	// ErrRouteNotFound is returned when no registered route matches the inbound request path.
	ErrRouteNotFound = errors.New("gateway: route not found")
	// ErrInvalidTarget is returned when a backend target URL cannot be parsed.
	ErrInvalidTarget = errors.New("gateway: invalid backend target URL")
)
