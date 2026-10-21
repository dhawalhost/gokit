// auth-jwt demonstrates JWT authentication using gokit's crypto and middleware
// packages.  It exposes a login endpoint that issues HS256 tokens and a group
// of protected routes that require a valid Bearer token.
//
// Run:
//
//	APP_JWT_SECRET="change-me-to-a-32+-byte-secret!!" \
//	APP_SERVER_ADDR=:8080 go run main.go
//
// Test:
//
//	# Login
//	TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
//	  -H "Content-Type: application/json" \
//	  -d '{"email":"alice@example.com","password":"password123"}' | jq -r .data.token)
//
//	# Access protected route
//	curl http://localhost:8080/api/v1/me -H "Authorization: Bearer $TOKEN"
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/crypto"
	apperrors "github.com/dhawalhost/gokit/errors"
	"github.com/dhawalhost/gokit/health"
	"github.com/dhawalhost/gokit/logger"
	mw "github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/observability"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/server"
	"github.com/dhawalhost/gokit/validator"
)

// ---------------------------------------------------------------------------
// User store (in-memory for the example; replace with a real DB in production)
// ---------------------------------------------------------------------------

type storedUser struct {
	ID           string
	Email        string
	PasswordHash string
	Roles        []string
}

// users is a pre-seeded user store.  In production this would be a database lookup.
var users map[string]storedUser

func init() {
	// Pre-hash "password123" using bcrypt (cost 12).
	hash, err := crypto.HashPassword("password123")
	if err != nil {
		panic("failed to hash seed password: " + err.Error())
	}
	users = map[string]storedUser{
		"alice@example.com": {
			ID:           "usr_01",
			Email:        "alice@example.com",
			PasswordHash: hash,
			Roles:        []string{"user"},
		},
		"admin@example.com": {
			ID:           "usr_00",
			Email:        "admin@example.com",
			PasswordHash: hash,
			Roles:        []string{"user", "admin"},
		},
	}
}

// ---------------------------------------------------------------------------
// Request / response types
// ---------------------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type profileResponse struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
}

// ---------------------------------------------------------------------------
// Handler dependencies
// ---------------------------------------------------------------------------

type authHandler struct {
	jwtSecret []byte
	jwtExpiry time.Duration
	jwtIssuer string
	val       *validator.Validator
	log       *zap.Logger
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	cfg := config.MustLoad(os.Getenv("CONFIG_FILE"))

	if cfg.JWT.Secret == "" {
		panic("APP_JWT_SECRET must be set to a value of at least 32 characters")
	}

	log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}
	defer func() { _ = log.Sync() }()
	logger.SetGlobal(log)

	serviceName := cfg.Telemetry.ServiceName
	if serviceName == "" {
		serviceName = "auth_jwt"
	}
	observability.InitMetrics(serviceName)

	ctx := context.Background()
	shutdownTracer, err := observability.InitTracer(ctx, cfg.Telemetry)
	if err != nil {
		log.Fatal("failed to initialise tracer", zap.Error(err))
	}
	defer func() { _ = shutdownTracer(ctx) }()

	issuer := cfg.JWT.Issuer
	if issuer == "" {
		issuer = serviceName
	}

	h := &authHandler{
		jwtSecret: []byte(cfg.JWT.Secret),
		jwtExpiry: cfg.JWT.Expiry,
		jwtIssuer: issuer,
		val:       validator.New(),
		log:       log,
	}

	healthHandler := health.NewHandler()

	srv := server.New(
		server.WithAddr(cfg.Server.Addr),
		server.WithReadTimeout(cfg.Server.ReadTimeout),
		server.WithWriteTimeout(cfg.Server.WriteTimeout),
		server.WithIdleTimeout(cfg.Server.IdleTimeout),
		server.WithShutdownTimeout(cfg.Server.ShutdownTimeout),
	)

	srv.Use(
		mw.RequestID(),
		mw.Recovery(log),
		mw.SecureHeaders(),
		mw.Logger(log),
		observability.Metrics(),
		observability.Tracing(serviceName),
	)

	srv.Mount("/health", buildHealthRouter(healthHandler))
	srv.Mount("/metrics", observability.MetricsHandler())
	srv.Mount("/api/v1", buildAPIRouter(h, cfg))

	log.Info("server listening", zap.String("addr", cfg.Server.Addr))
	if err := srv.Run(ctx); err != nil {
		log.Fatal("server error", zap.Error(err))
	}
}

// ---------------------------------------------------------------------------
// Routers
// ---------------------------------------------------------------------------

func buildHealthRouter(h *health.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/live", h.LiveHandler())
	r.Get("/ready", h.ReadyHandler())
	return r
}

func buildAPIRouter(h *authHandler, cfg *config.Config) http.Handler {
	r := chi.NewRouter()
	r.Use(mw.Timeout(cfg.Server.WriteTimeout - 5*time.Second))

	// Public endpoints — no authentication required.
	r.Post("/auth/login", h.login)

	// Protected endpoints — require a valid Bearer token.
	jwtMiddleware := mw.JWT(mw.JWTConfig{
		SecretKey:  []byte(cfg.JWT.Secret),
		Algorithm:  "HS256",
		ContextKey: "claims",
	})
	r.Group(func(r chi.Router) {
		r.Use(jwtMiddleware)
		r.Get("/me", h.profile)
		r.Post("/auth/logout", h.logout) // stateless logout — instruct client to discard token
	})

	return r
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// login validates credentials and returns a signed JWT.
//
// POST /api/v1/auth/login.
func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := h.val.Bind(r, &req); err != nil {
		apperrors.WriteError(w, r, apperrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	user, ok := users[req.Email]
	if !ok {
		// Return the same error for unknown email and wrong password to prevent
		// user-enumeration attacks.
		apperrors.WriteError(w, r, apperrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password"))
		return
	}

	if err := crypto.CheckPassword(req.Password, user.PasswordHash); err != nil {
		apperrors.WriteError(w, r, apperrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password"))
		return
	}

	expiresAt := time.Now().Add(h.jwtExpiry)
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    h.jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID:   user.ID,
		TenantID: "",
		Roles:    user.Roles,
	}

	token, err := crypto.SignHS256(claims, h.jwtSecret)
	if err != nil {
		h.log.Error("failed to sign token", zap.Error(err))
		apperrors.WriteError(w, r, apperrors.Internal("failed to generate token"))
		return
	}

	response.Ok(w, r, loginResponse{Token: token, ExpiresAt: expiresAt})
}

// profile returns the authenticated user's identity.
//
// GET /api/v1/me.
func (h *authHandler) profile(w http.ResponseWriter, r *http.Request) {
	claims, ok := mw.ClaimsFromContext(r.Context())
	if !ok {
		apperrors.WriteError(w, r, apperrors.Unauthorized("UNAUTHORIZED", "missing token claims"))
		return
	}

	userID, _ := claims["user_id"].(string)
	email, _ := claims.GetSubject()
	rawRoles, _ := claims["roles"].([]interface{})

	roles := make([]string, 0, len(rawRoles))
	for _, v := range rawRoles {
		if s, ok := v.(string); ok {
			roles = append(roles, s)
		}
	}

	response.Ok(w, r, profileResponse{
		UserID: userID,
		Email:  email,
		Roles:  roles,
	})
}

// logout is a stateless logout — the client must discard the token.
// For true revocation, maintain a token denylist (e.g. in Redis).
//
// POST /api/v1/auth/logout.
func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	response.Ok(w, r, map[string]string{"message": "logged out successfully"})
}
