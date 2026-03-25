// Package response provides generic HTTP JSON response helpers using Go generics.
package response

import (
	"encoding/json"
	"net/http"

	"github.com/dhawalhost/gokit/middleware"
)

// Response is a generic success envelope.
type Response[T any] struct {
	Success   bool   `json:"success"`
	Data      T      `json:"data,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// PaginatedResponse is a generic paginated success envelope.
type PaginatedResponse[T any] struct {
	Success    bool       `json:"success"`
	Data       []T        `json:"data"`
	Pagination Pagination `json:"pagination"`
	RequestID  string     `json:"request_id,omitempty"`
}

// Pagination holds paging metadata.
type Pagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// JSON writes a generic JSON response.
func JSON[T any](w http.ResponseWriter, r *http.Request, status int, data T) {
	write(w, status, Response[T]{
		Success:   status < 400,
		Data:      data,
		RequestID: middleware.RequestIDFromContext(r.Context()),
	})
}

// Ok writes a 200 JSON response.
func Ok[T any](w http.ResponseWriter, r *http.Request, data T) {
	JSON[T](w, r, http.StatusOK, data)
}

// Created writes a 201 JSON response.
func Created[T any](w http.ResponseWriter, r *http.Request, data T) {
	JSON[T](w, r, http.StatusCreated, data)
}

// NoContent writes a 204 response with no body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Paginated writes a paginated 200 JSON response.
func Paginated[T any](w http.ResponseWriter, r *http.Request, data []T, p Pagination) {
	write(w, http.StatusOK, PaginatedResponse[T]{
		Success:    true,
		Data:       data,
		Pagination: p,
		RequestID:  middleware.RequestIDFromContext(r.Context()),
	})
}
