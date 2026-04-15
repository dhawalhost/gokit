// database-crud demonstrates a production-ready RESTful CRUD API backed by
// PostgreSQL using GORM.  It covers validation, paginated listing, structured
// errors, health checks, and safe parameterised queries.
//
// Run:
//
//	APP_DATABASE_DSN="postgres://user:pass@localhost:5432/app?sslmode=disable" \
//	APP_SERVER_ADDR=:8080 go run main.go
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
	apperrors "github.com/dhawalhost/gokit/errors"
	"github.com/dhawalhost/gokit/health"
	"github.com/dhawalhost/gokit/logger"
	mw "github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/observability"
	"github.com/dhawalhost/gokit/pagination"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/server"
	"github.com/dhawalhost/gokit/validator"
)

// ---------------------------------------------------------------------------
// Domain model
// ---------------------------------------------------------------------------

// User is the GORM model for the users table.
type User struct {
	ID        uint           `gorm:"primarykey"          json:"id"`
	Name      string         `gorm:"not null"            json:"name"`
	Email     string         `gorm:"uniqueIndex;not null" json:"email"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"               json:"-"` // soft-delete
}

// ---------------------------------------------------------------------------
// Request/response types
// ---------------------------------------------------------------------------

type createUserRequest struct {
	Name  string `json:"name"  validate:"required,min=2,max=100"`
	Email string `json:"email" validate:"required,email"`
}

type updateUserRequest struct {
	Name  string `json:"name"  validate:"omitempty,min=2,max=100"`
	Email string `json:"email" validate:"omitempty,email"`
}

// ---------------------------------------------------------------------------
// Handler dependencies
// ---------------------------------------------------------------------------

type userHandler struct {
	db  *database.DB
	val *validator.Validator
	log *zap.Logger
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	cfg := config.MustLoad(os.Getenv("CONFIG_FILE"))

	log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}
	defer func() { _ = log.Sync() }()
	logger.SetGlobal(log)

	serviceName := cfg.Telemetry.ServiceName
	if serviceName == "" {
		serviceName = "database_crud"
	}
	observability.InitMetrics(serviceName)

	ctx := context.Background()
	shutdownTracer, err := observability.InitTracer(ctx, cfg.Telemetry)
	if err != nil {
		log.Fatal("failed to initialise tracer", zap.Error(err))
	}
	defer func() { _ = shutdownTracer(ctx) }()

	if cfg.Database.DSN == "" {
		log.Fatal("APP_DATABASE_DSN is required")
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		log.Fatal("failed to connect to database", zap.Error(err))
	}
	defer func() { _ = db.Close() }()

	// Auto-migrate in development; use proper migrations in production.
	if cfg.Log.Development {
		if err := db.GORM.AutoMigrate(&User{}); err != nil {
			log.Fatal("auto-migrate failed", zap.Error(err))
		}
	}

	healthHandler := health.NewHandler()
	healthHandler.Register("postgres", db)

	h := &userHandler{db: db, val: validator.New(), log: log}

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
	srv.Mount("/api/v1", buildAPIRouter(h, cfg.Server.WriteTimeout))

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

func buildAPIRouter(h *userHandler, writeTimeout time.Duration) http.Handler {
	r := chi.NewRouter()
	r.Use(mw.Timeout(writeTimeout - 5*time.Second))

	r.Route("/users", func(r chi.Router) {
		r.Get("/", h.listUsers)
		r.Post("/", h.createUser)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getUser)
			r.Patch("/", h.updateUser)
			r.Delete("/", h.deleteUser)
		})
	})
	return r
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// listUsers returns a paginated list of users.
//
// GET /api/v1/users?page=1&page_size=20.
func (h *userHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	pg := pagination.ParseOffsetParams(r)

	var total int64
	if err := h.db.GORM.Model(&User{}).Count(&total).Error; err != nil {
		apperrors.WriteError(w, r, apperrors.Internal("failed to count users"))
		return
	}

	var users []User
	if err := pg.Apply(h.db.GORM).Order("id asc").Find(&users).Error; err != nil {
		apperrors.WriteError(w, r, apperrors.Internal("failed to list users"))
		return
	}

	response.Paginated(w, r, users, pg.ToPagination(total))
}

// createUser creates a new user.
//
// POST /api/v1/users.
func (h *userHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := h.val.Bind(r, &req); err != nil {
		apperrors.WriteError(w, r, apperrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	user := User{Name: req.Name, Email: req.Email}
	if err := h.db.GORM.Create(&user).Error; err != nil {
		// Detect unique-constraint violations without relying on string matching.
		if isDuplicateKeyError(err) {
			apperrors.WriteError(w, r, apperrors.Conflict("EMAIL_TAKEN", "email address is already registered"))
			return
		}
		h.log.Error("create user failed", zap.Error(err), zap.String("email", req.Email))
		apperrors.WriteError(w, r, apperrors.Internal("failed to create user"))
		return
	}

	response.Created(w, r, user)
}

// getUser returns a single user by ID.
//
// GET /api/v1/users/{id}.
func (h *userHandler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var user User
	// SAFE: parameterised query — never interpolate user input into SQL.
	if err := h.db.GORM.First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			apperrors.WriteError(w, r, apperrors.NotFound("USER_NOT_FOUND", "user not found"))
			return
		}
		apperrors.WriteError(w, r, apperrors.Internal("failed to fetch user"))
		return
	}

	response.Ok(w, r, user)
}

// updateUser partially updates a user.
//
// PATCH /api/v1/users/{id}.
func (h *userHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var req updateUserRequest
	if err := h.val.Bind(r, &req); err != nil {
		apperrors.WriteError(w, r, apperrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	var user User
	if err := h.db.GORM.First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			apperrors.WriteError(w, r, apperrors.NotFound("USER_NOT_FOUND", "user not found"))
			return
		}
		apperrors.WriteError(w, r, apperrors.Internal("failed to fetch user"))
		return
	}

	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Email != "" {
		updates["email"] = req.Email
	}

	if len(updates) == 0 {
		response.Ok(w, r, user)
		return
	}

	if err := h.db.GORM.Model(&user).Updates(updates).Error; err != nil {
		if isDuplicateKeyError(err) {
			apperrors.WriteError(w, r, apperrors.Conflict("EMAIL_TAKEN", "email address is already registered"))
			return
		}
		apperrors.WriteError(w, r, apperrors.Internal("failed to update user"))
		return
	}

	response.Ok(w, r, user)
}

// deleteUser soft-deletes a user.
//
// DELETE /api/v1/users/{id}.
func (h *userHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	result := h.db.GORM.Delete(&User{}, "id = ?", id)
	if result.Error != nil {
		apperrors.WriteError(w, r, apperrors.Internal("failed to delete user"))
		return
	}
	if result.RowsAffected == 0 {
		apperrors.WriteError(w, r, apperrors.NotFound("USER_NOT_FOUND", "user not found"))
		return
	}

	response.NoContent(w)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseIDParam extracts and validates the {id} path parameter.
func parseIDParam(w http.ResponseWriter, r *http.Request) (uint, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		apperrors.WriteError(w, r, apperrors.BadRequest("INVALID_ID", "id must be a positive integer"))
		return 0, false
	}
	return uint(id), true
}

// isDuplicateKeyError reports whether err is a PostgreSQL unique-constraint violation.
// This avoids fragile string matching against driver error messages.
func isDuplicateKeyError(err error) bool {
	// pgx encodes the SQLSTATE code in the error; code 23505 = unique_violation.
	type pgError interface{ SQLState() string }
	var pg pgError
	if errors.As(err, &pg) {
		return pg.SQLState() == "23505"
	}
	return false
}
