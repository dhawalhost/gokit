package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RouteConfig defines forwarding rules for a path prefix to one or more backend targets.
type RouteConfig struct {
	PathPrefix   string
	StripPrefix  bool
	Targets      []string
	Balancer     Balancer
	Headers      map[string]string
	Timeout      time.Duration
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

type route struct {
	pathPrefix   string
	stripPrefix  bool
	balancer     Balancer
	targets      []*Target
	headers      map[string]string
	timeout      time.Duration
	errorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

// Proxy coordinates reverse proxy routing and load balancing to downstream services.
type Proxy struct {
	mu     sync.RWMutex
	routes []*route
}

// NewProxy creates an HTTP reverse proxy configured with the given route rules.
func NewProxy(routes ...RouteConfig) (*Proxy, error) {
	p := &Proxy{}
	for _, cfg := range routes {
		if err := p.AddRoute(cfg); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// AddRoute adds a forwarding route to the proxy.
func (p *Proxy) AddRoute(cfg RouteConfig) error {
	if cfg.PathPrefix == "" {
		return fmt.Errorf("%w: path prefix cannot be empty", ErrRouteNotFound)
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("%w: at least one target required for route %s", ErrNoHealthyTargets, cfg.PathPrefix)
	}

	targets := make([]*Target, 0, len(cfg.Targets))
	for _, rawURL := range cfg.Targets {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("%w: %s", ErrInvalidTarget, rawURL)
		}

		target := NewTarget(parsed)
		target.ReverseProxy = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(parsed)
				pr.SetXForwarded()
				for k, v := range cfg.Headers {
					pr.Out.Header.Set(k, v)
				}
			},
		}

		targets = append(targets, target)
	}

	balancer := cfg.Balancer
	if balancer == nil {
		balancer = NewRoundRobinBalancer()
	}

	r := &route{
		pathPrefix:   cfg.PathPrefix,
		stripPrefix:  cfg.StripPrefix,
		balancer:     balancer,
		targets:      targets,
		headers:      cfg.Headers,
		timeout:      cfg.Timeout,
		errorHandler: cfg.ErrorHandler,
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Insert keeping longest prefix first for greedy matching
	inserted := false
	for i, existing := range p.routes {
		if len(r.pathPrefix) > len(existing.pathPrefix) {
			p.routes = append(p.routes[:i], append([]*route{r}, p.routes[i:]...)...)
			inserted = true
			break
		}
	}
	if !inserted {
		p.routes = append(p.routes, r)
	}

	return nil
}

// ServeHTTP handles incoming requests, matches route prefixes, and proxies to healthy targets.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := p.matchRoute(r.URL.Path)
	if route == nil {
		http.Error(w, ErrRouteNotFound.Error(), http.StatusNotFound)
		return
	}

	target, err := route.balancer.Next(route.targets)
	if err != nil {
		if route.errorHandler != nil {
			route.errorHandler(w, r, err)
			return
		}
		http.Error(w, ErrNoHealthyTargets.Error(), http.StatusBadGateway)
		return
	}

	req := r
	if route.timeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), route.timeout)
		defer cancel()
		req = r.WithContext(ctx)
	}

	if route.stripPrefix {
		trimmed := strings.TrimPrefix(req.URL.Path, route.pathPrefix)
		if !strings.HasPrefix(trimmed, "/") {
			trimmed = "/" + trimmed
		}
		req.URL.Path = trimmed
	}

	target.ActiveConns.Add(1)
	defer target.ActiveConns.Add(-1)

	target.ReverseProxy.ServeHTTP(w, req)
}

func (p *Proxy) matchRoute(path string) *route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, r := range p.routes {
		if strings.HasPrefix(path, r.pathPrefix) {
			return r
		}
	}
	return nil
}
