package multitenancy

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// DefaultRLSVariable is the standard PostgreSQL session setting name for Row-Level Security tenant isolation.
const DefaultRLSVariable = "app.current_tenant"

// TenantModel can be embedded in GORM models to provide a standardized tenant_id field.
type TenantModel struct {
	TenantID string `gorm:"size:64;index;not null" json:"tenant_id"`
}

// GetTenantID returns the tenant ID.
func (m *TenantModel) GetTenantID() string {
	return m.TenantID
}

// SetTenantID updates the tenant ID.
func (m *TenantModel) SetTenantID(id string) {
	m.TenantID = id
}

// TenantScoped is an interface implemented by entities that belong to a tenant.
type TenantScoped interface {
	GetTenantID() string
	SetTenantID(id string)
}

// Scope applies a WHERE tenant_id = ? filter to queries.
func Scope(tenantID string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("tenant_id = ?", tenantID)
	}
}

// ScopeFromContext extracts the tenant ID from context and applies WHERE tenant_id = ?.
func ScopeFromContext(ctx context.Context) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if tid, ok := FromContext(ctx); ok {
			return db.Where("tenant_id = ?", tid)
		}
		return db
	}
}

// SetRLS sets the PostgreSQL session variable for Row-Level Security inside an active transaction.
func SetRLS(ctx context.Context, tx *gorm.DB, tenantID string, variable ...string) error {
	v := DefaultRLSVariable
	if len(variable) > 0 && variable[0] != "" {
		v = variable[0]
	}
	// SET LOCAL is transaction-scoped in PostgreSQL and automatically reverts on commit/rollback.
	return tx.WithContext(ctx).Exec(fmt.Sprintf("SET LOCAL %s = ?", v), tenantID).Error
}

// WithTenantTx executes fn within a database transaction where PostgreSQL RLS is configured for the given tenant.
func WithTenantTx(ctx context.Context, db *gorm.DB, tenantID string, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := SetRLS(ctx, tx, tenantID); err != nil {
			return fmt.Errorf("multitenancy: set rls tenant: %w", err)
		}
		return fn(tx)
	})
}
