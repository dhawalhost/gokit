# Enterprise Microservice Example

This example demonstrates how to build an enterprise-grade, SOC2-compliant, multi-tenant microservice using `gokit`.

---

## Architecture & Features Covered

1. **Multi-Tenancy (`multitenancy`)**:
   - `multitenancy.RequireTenant()` strictly enforces the `X-Tenant-ID` header on all API routes.
   - All invoices and events are isolated per tenant.

2. **Fine-Grained Authorization (`authz`)**:
   - `authz.Enforcer` evaluates role permissions (`admin`, `finance`, `viewer`).
   - `authz.Require(enforcer, "invoices:create")` protects the creation endpoint.
   - `authz.Require(enforcer, "invoices:read")` protects querying.

3. **KMS Envelope Encryption (`crypto/kms`)**:
   - Sensitive bank account numbers are encrypted at rest using per-record Data Encryption Keys (DEKs).
   - Only the masked account number (`****1234`) and encrypted envelope are retained.

4. **Distributed Locking (`distlock`)**:
   - `distlock.WithLock(ctx, "tenant:X:invoice:Y", ...)` prevents concurrent duplicate invoice generation.

5. **Transactional Outbox (`outbox`)**:
   - Business state mutations and outbox event records are saved in the same transaction.
   - A background `outbox.Relay` polls pending events and reliably dispatches them to the message broker.

6. **Audit Logging (`audit`)**:
   - Non-blocking `audit.AsyncSink` captures Actor, Resource, and Changes.
   - Automatically sanitizes PII and secret attributes.

---

## Running the Service

```bash
cd examples/enterprise-service
go run main.go
```

---

## Testing the Endpoints

### 1. Create an Invoice (Role: `finance`, Tenant: `acme-corp`)

```bash
curl -X POST http://localhost:8080/api/v1/invoices \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: acme-corp" \
  -H "X-User-ID: usr-101" \
  -H "X-User-Role: finance" \
  -d '{
    "invoice_number": "INV-2026-001",
    "amount": 4999.00,
    "currency": "USD",
    "bank_account_num": "98765432101234"
  }'
```

**Expected Response:**
```json
{
  "code": "CREATED",
  "data": {
    "id": "0192e21b-...",
    "tenant_id": "acme-corp",
    "invoice_number": "INV-2026-001",
    "amount": 4999,
    "currency": "USD",
    "encrypted_account_no": "eyJhbGci...",
    "masked_account_no": "****1234",
    "created_at": "..."
  }
}
```

### 2. Query Invoices for Tenant `acme-corp`

```bash
curl http://localhost:8080/api/v1/invoices \
  -H "X-Tenant-ID: acme-corp" \
  -H "X-User-Role: viewer"
```

### 3. Verify Tenant Isolation (Tenant: `other-corp`)

```bash
curl http://localhost:8080/api/v1/invoices \
  -H "X-Tenant-ID: other-corp" \
  -H "X-User-Role: viewer"
```
**Expected Response:** `[]` (Returns empty list; cannot see `acme-corp` data).

### 4. Verify Authorization Rejection (Role: `viewer` attempting `POST`)

```bash
curl -X POST http://localhost:8080/api/v1/invoices \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: acme-corp" \
  -H "X-User-Role: viewer" \
  -d '{"invoice_number":"INV-FAIL"}'
```
**Expected Response:** HTTP 403 Forbidden:
```json
{"code":"FORBIDDEN","message":"insufficient permissions"}
```
