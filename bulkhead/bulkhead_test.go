package bulkhead_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/bulkhead"
)

func TestBulkhead_Execute(t *testing.T) {
	tests := []struct {
		name          string
		maxConcurrent int
		maxQueue      int
		totalSpawns   int
		holdDuration  time.Duration
		expectRejects bool
	}{
		{
			name:          "concurrency within limit succeeds",
			maxConcurrent: 5,
			maxQueue:      0,
			totalSpawns:   3,
			holdDuration:  10 * time.Millisecond,
			expectRejects: false,
		},
		{
			name:          "burst exceeding concurrency without queue rejects immediately",
			maxConcurrent: 2,
			maxQueue:      0,
			totalSpawns:   5,
			holdDuration:  50 * time.Millisecond,
			expectRejects: true,
		},
		{
			name:          "burst within queue capacity succeeds eventually",
			maxConcurrent: 2,
			maxQueue:      3,
			totalSpawns:   4,
			holdDuration:  20 * time.Millisecond,
			expectRejects: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := bulkhead.New(bulkhead.Config{
				MaxConcurrent: tc.maxConcurrent,
				MaxQueue:      tc.maxQueue,
			})

			var wg sync.WaitGroup
			var rejections atomic.Int32
			var successes atomic.Int32

			for i := 0; i < tc.totalSpawns; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := b.Execute(context.Background(), func() error {
						time.Sleep(tc.holdDuration)
						return nil
					})
					if err != nil {
						rejections.Add(1)
					} else {
						successes.Add(1)
					}
				}()
			}

			wg.Wait()

			if tc.expectRejects && rejections.Load() == 0 {
				t.Errorf("expected rejections, got 0")
			}
			if !tc.expectRejects && rejections.Load() > 0 {
				t.Errorf("expected 0 rejections, got %d", rejections.Load())
			}
		})
	}
}

func TestPartitionedBulkhead_TenantIsolation(t *testing.T) {
	pb := bulkhead.NewPartitionedBulkhead(bulkhead.Config{
		MaxConcurrent: 1, // Only 1 concurrent request per tenant
		MaxQueue:      0,
	})

	// Tenant A holds its slot
	heldA := make(chan struct{})
	doneA := make(chan struct{})

	go func() {
		_ = pb.Get("tenant-a").Execute(context.Background(), func() error {
			close(heldA)
			time.Sleep(100 * time.Millisecond)
			close(doneA)
			return nil
		})
	}()

	<-heldA

	// Second request for Tenant A should be rejected immediately
	errA := pb.Get("tenant-a").Execute(context.Background(), func() error {
		return nil
	})
	if errA != bulkhead.ErrBulkheadFull {
		t.Errorf("expected ErrBulkheadFull for tenant-a, got %v", errA)
	}

	// Request for Tenant B should SUCCEED even while Tenant A is busy
	errB := pb.Get("tenant-b").Execute(context.Background(), func() error {
		return nil
	})
	if errB != nil {
		t.Errorf("tenant-b should not be blocked by tenant-a: %v", errB)
	}

	<-doneA
}

func TestBulkhead_Middleware(t *testing.T) {
	pb := bulkhead.NewPartitionedBulkhead(bulkhead.Config{
		MaxConcurrent: 1,
		MaxQueue:      0,
	})

	mw := bulkhead.Middleware(pb, func(r *http.Request) string {
		return r.Header.Get("X-Tenant-ID")
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	var statuses []int
	var mu sync.Mutex

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Tenant-ID", "tenant-1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			mu.Lock()
			statuses = append(statuses, rec.Code)
			mu.Unlock()
		}()
	}

	wg.Wait()

	var has429 bool
	for _, code := range statuses {
		if code == http.StatusTooManyRequests {
			has429 = true
			break
		}
	}

	if !has429 {
		t.Errorf("expected at least one 429 Too Many Requests response, got: %v", statuses)
	}
}
