package multitenancy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

// DefaultRLSVariable is the standard PostgreSQL session setting name for Row-Level Security tenant isolation.
const DefaultRLSVariable = "app.current_tenant"

// TenantModel can be embedded in ORM models to provide a standardized tenant_id field.
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

// Scope applies a WHERE tenant_id = ? filter to query builders (such as *gorm.DB).
func Scope[DB interface{ Where(any, ...any) DB }](tenantID string) func(db DB) DB {
	return func(db DB) DB {
		return db.Where("tenant_id = ?", tenantID)
	}
}

// ScopeFromContext extracts the tenant ID from context and applies WHERE tenant_id = ?.
func ScopeFromContext[DB interface{ Where(any, ...any) DB }](ctx context.Context) func(db DB) DB {
	return func(db DB) DB {
		if tid, ok := FromContext(ctx); ok {
			return db.Where("tenant_id = ?", tid)
		}
		return db
	}
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetRLS sets the PostgreSQL session variable for Row-Level Security inside an active transaction.
// It supports standard database/sql *sql.Tx / *sql.Conn handles as well as ORM handles (such as *gorm.DB).
func SetRLS(ctx context.Context, tx any, tenantID string, variable ...string) error {
	if tx == nil {
		return errors.New("multitenancy: nil transaction handle")
	}

	v := DefaultRLSVariable
	if len(variable) > 0 && variable[0] != "" {
		v = variable[0]
	}

	if execer, ok := tx.(sqlExecer); ok {
		_, err := execer.ExecContext(ctx, "SELECT set_config($1, $2, true)", v, tenantID)
		return err
	}

	rv := reflect.ValueOf(tx)
	if !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return errors.New("multitenancy: nil transaction handle")
	}

	if wc := rv.MethodByName("WithContext"); wc.IsValid() && wc.Type().NumIn() == 1 && wc.Type().NumOut() == 1 {
		rv = wc.Call([]reflect.Value{reflect.ValueOf(ctx)})[0]
	}

	execMethod := rv.MethodByName("Exec")
	if !execMethod.IsValid() {
		return fmt.Errorf("multitenancy: unsupported tx handle %T for SetRLS", tx)
	}

	// set_config(name, value, is_local) safely sets transaction-scoped config with query parameters.
	results := execMethod.Call([]reflect.Value{
		reflect.ValueOf("SELECT set_config(?, ?, true)"),
		reflect.ValueOf(v),
		reflect.ValueOf(tenantID),
	})
	if len(results) == 0 {
		return nil
	}

	res := results[0]
	if res.Kind() == reflect.Pointer {
		if res.IsNil() {
			return nil
		}
		res = res.Elem()
	}
	if res.Kind() == reflect.Struct {
		errField := res.FieldByName("Error")
		if errField.IsValid() && !errField.IsNil() {
			if err, ok := errField.Interface().(error); ok {
				return err
			}
		}
	}
	return nil
}

// WithTenantTx executes fn within a database transaction where PostgreSQL RLS is configured for the given tenant.
// It accepts ORM handles (such as *gorm.DB) exposing a Transaction(func(tx DB) error, ...) method.
func WithTenantTx[DB any](ctx context.Context, db DB, tenantID string, fn func(tx DB) error) error {
	rv := reflect.ValueOf(db)
	if !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return errors.New("multitenancy: nil db handle")
	}

	if wc := rv.MethodByName("WithContext"); wc.IsValid() && wc.Type().NumIn() == 1 && wc.Type().NumOut() == 1 {
		next := wc.Call([]reflect.Value{reflect.ValueOf(ctx)})[0]
		if next.Type().AssignableTo(rv.Type()) {
			rv = next
		}
	}

	txMethod := rv.MethodByName("Transaction")
	if !txMethod.IsValid() || txMethod.Type().NumIn() < 1 {
		return fmt.Errorf("multitenancy: db handle %T does not support Transaction", db)
	}

	cbType := txMethod.Type().In(0)
	if cbType.Kind() != reflect.Func || cbType.NumIn() != 1 || cbType.NumOut() != 1 {
		return fmt.Errorf("multitenancy: unexpected Transaction callback signature %s", cbType)
	}

	errInterfaceType := reflect.TypeOf((*error)(nil)).Elem()
	cb := reflect.MakeFunc(cbType, func(args []reflect.Value) []reflect.Value {
		txArg, ok := args[0].Interface().(DB)
		if !ok {
			return []reflect.Value{reflect.ValueOf(fmt.Errorf("multitenancy: unexpected tx type %T", args[0].Interface())).Convert(errInterfaceType)}
		}
		if err := SetRLS(ctx, txArg, tenantID); err != nil {
			return []reflect.Value{reflect.ValueOf(fmt.Errorf("multitenancy: set rls tenant: %w", err)).Convert(errInterfaceType)}
		}
		if err := fn(txArg); err != nil {
			return []reflect.Value{reflect.ValueOf(err).Convert(errInterfaceType)}
		}
		return []reflect.Value{reflect.Zero(errInterfaceType)}
	})

	out := txMethod.Call([]reflect.Value{cb})
	if len(out) > 0 && !out[0].IsNil() {
		if err, ok := out[0].Interface().(error); ok {
			return err
		}
	}
	return nil
}
