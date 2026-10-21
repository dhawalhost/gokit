//go:build integration
// +build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
	"github.com/dhawalhost/gokit/multitenancy"
	"gorm.io/gorm"
)

type TenantDocument struct {
	ID       string `gorm:"primaryKey"`
	TenantID string `gorm:"not null"`
	Title    string `gorm:"not null"`
}

func (TenantDocument) TableName() string {
	return "tenant_documents"
}

func TestMultitenancy_PostgreSQLRowLevelSecurity(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	db, err := database.New(ctx, config.DatabaseConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	// 1. Setup table, non-superuser role (superusers bypass RLS), and PostgreSQL Row-Level Security policy
	setupSQL := `
	DO $$
	BEGIN
		IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'tenant_app_user') THEN
			CREATE ROLE tenant_app_user;
		END IF;
	END
	$$;
	GRANT USAGE ON SCHEMA public TO tenant_app_user;

	CREATE TABLE IF NOT EXISTS tenant_documents (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		title TEXT NOT NULL
	);
	GRANT ALL PRIVILEGES ON TABLE tenant_documents TO tenant_app_user;

	ALTER TABLE tenant_documents ENABLE ROW LEVEL SECURITY;
	ALTER TABLE tenant_documents FORCE ROW LEVEL SECURITY;
	DROP POLICY IF EXISTS tenant_isolation_policy ON tenant_documents;
	CREATE POLICY tenant_isolation_policy ON tenant_documents
		USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), ''))
		WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), ''));
	`
	if err := db.GORM.Exec(setupSQL).Error; err != nil {
		t.Fatalf("setup RLS table: %v", err)
	}
	defer func() {
		_ = db.GORM.Exec("DROP TABLE IF EXISTS tenant_documents").Error
	}()

	withTenant := func(tenantID string, fn func(tx *gorm.DB) error) error {
		return multitenancy.WithTenantTx(ctx, db.GORM, tenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL ROLE tenant_app_user").Error; err != nil {
				return err
			}
			return fn(tx)
		})
	}

	// 2. Insert documents as Tenant A
	err = withTenant("tenant-a", func(tx *gorm.DB) error {
		doc := &TenantDocument{
			ID:       "doc-a1",
			TenantID: "tenant-a",
			Title:    "Tenant A Secret Document",
		}
		return tx.Create(doc).Error
	})
	if err != nil {
		t.Fatalf("insert tenant-a doc: %v", err)
	}

	// 3. Query documents as Tenant A -> should find doc-a1
	err = withTenant("tenant-a", func(tx *gorm.DB) error {
		var docs []TenantDocument
		if err := tx.Find(&docs).Error; err != nil {
			return err
		}
		if len(docs) != 1 || docs[0].ID != "doc-a1" {
			t.Errorf("tenant-a expected to see doc-a1, got %v", docs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("query tenant-a doc: %v", err)
	}

	// 4. Query documents as Tenant B -> RLS MUST filter out Tenant A's document (returns 0 rows)
	err = withTenant("tenant-b", func(tx *gorm.DB) error {
		var docs []TenantDocument
		if err := tx.Find(&docs).Error; err != nil {
			return err
		}
		if len(docs) != 0 {
			t.Errorf("SECURITY BREACH: tenant-b saw tenant-a documents: %+v", docs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("query tenant-b doc: %v", err)
	}

	// 5. Query without RLS tenant session set -> RLS policy filters out all rows
	err = db.GORM.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE tenant_app_user").Error; err != nil {
			return err
		}
		var docs []TenantDocument
		if err := tx.Find(&docs).Error; err != nil {
			return err
		}
		if len(docs) != 0 {
			t.Errorf("expected 0 docs when app.current_tenant is unset, got %+v", docs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("query without tenant session: %v", err)
	}
}

func TestMultitenancy_GORMScope(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	db, err := database.New(ctx, config.DatabaseConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	if err := db.GORM.AutoMigrate(&TenantDocument{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Seed documents for multiple tenants
	_ = db.GORM.Exec("DELETE FROM tenant_documents").Error
	_ = db.GORM.Create(&TenantDocument{ID: "d1", TenantID: "org-1", Title: "Org 1 Doc"}).Error
	_ = db.GORM.Create(&TenantDocument{ID: "d2", TenantID: "org-2", Title: "Org 2 Doc"}).Error

	// Query with Scope
	var org1Docs []TenantDocument
	if err := db.GORM.Scopes(multitenancy.Scope("org-1")).Find(&org1Docs).Error; err != nil {
		t.Fatalf("Scope find: %v", err)
	}
	if len(org1Docs) != 1 || org1Docs[0].ID != "d1" {
		t.Errorf("expected only org-1 doc, got %+v", org1Docs)
	}

	// Query with ScopeFromContext
	tenantCtx := multitenancy.WithTenantID(ctx, "org-2")
	var org2Docs []TenantDocument
	if err := db.GORM.WithContext(tenantCtx).Scopes(multitenancy.ScopeFromContext(tenantCtx)).Find(&org2Docs).Error; err != nil {
		t.Fatalf("ScopeFromContext find: %v", err)
	}
	if len(org2Docs) != 1 || org2Docs[0].ID != "d2" {
		t.Errorf("expected only org-2 doc, got %+v", org2Docs)
	}
}
