package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ServiceRouter is implemented by components that expose a sub-router at a
// fixed URL pattern.
type ServiceRouter interface {
	// Pattern returns the mount path (e.g. "/api/v1/users").
	Pattern() string
	// Router returns the http.Handler for this service.
	Router() http.Handler
}

// Mount mounts each ServiceRouter onto mux at its declared pattern.
func Mount(mux *chi.Mux, services ...ServiceRouter) {
	for _, svc := range services {
		mux.Mount(svc.Pattern(), svc.Router())
	}
}
