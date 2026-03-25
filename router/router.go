// Package router provides chi router constructors for the gokit library.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New returns a new chi.Mux with no middleware pre-installed.
func New() *chi.Mux {
	return chi.NewRouter()
}

// NewWithMiddleware returns a new chi.Mux pre-loaded with the given middleware.
func NewWithMiddleware(middleware ...func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware...)
	return r
}
