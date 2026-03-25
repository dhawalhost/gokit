package server

import (
	"context"
	"net/http"
	"time"
)

// other existing code...

func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Set ReadHeaderTimeout
	srv.ReadHeaderTimeout = 5 * time.Second

	return srv.Shutdown(ctx)
}