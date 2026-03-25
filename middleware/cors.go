package middleware

import (
	"net/http"

	chiCors "github.com/go-chi/cors"
)

// CORS returns a middleware that applies CORS policy with the given allowed origins.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return chiCors.Handler(chiCors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID", "X-API-Key"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	})
}

// CORSAllowAll returns a CORS middleware that permits all origins.
func CORSAllowAll() func(http.Handler) http.Handler {
	return chiCors.AllowAll().Handler
}
