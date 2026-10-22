package loadshed_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dhawalhost/gokit/loadshed"
)

func TestLimiter_AcquireAndRelease(t *testing.T) {
	lim := loadshed.NewLimiter(
		loadshed.WithInitialLimit(2.0),
		loadshed.WithMinLimit(1.0),
		loadshed.WithMaxLimit(10.0),
	)

	ctx := context.Background()

	// 1. Acquire 1st request
	done1, err := lim.Acquire(ctx, loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}

	// 2. Acquire 2nd request
	done2, err := lim.Acquire(ctx, loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("acquire 2 failed: %v", err)
	}

	// 3. Acquire 3rd request (should be shed)
	_, err = lim.Acquire(ctx, loadshed.PriorityStandard)
	if !errors.Is(err, loadshed.ErrLoadShed) {
		t.Fatalf("expected ErrLoadShed on 3rd request, got %v", err)
	}

	// 4. Critical request should be admitted with headroom (+20% of 2.0 = 2.4, inFlight 2 < 2.4)
	doneCrit, err := lim.Acquire(ctx, loadshed.PriorityCritical)
	if err != nil {
		t.Fatalf("expected critical request to be admitted under headroom: %v", err)
	}

	stats := lim.Stats()
	if stats.InFlight != 3 {
		t.Errorf("expected 3 in-flight requests, got %d", stats.InFlight)
	}
	if stats.TotalAdmitted != 3 {
		t.Errorf("expected 3 total admitted, got %d", stats.TotalAdmitted)
	}
	if stats.TotalShed != 1 {
		t.Errorf("expected 1 total shed, got %d", stats.TotalShed)
	}

	// Release requests
	doneCrit(nil)
	done2(nil)
	done1(nil)

	statsAfter := lim.Stats()
	if statsAfter.InFlight != 0 {
		t.Errorf("expected 0 in-flight after releases, got %d", statsAfter.InFlight)
	}
}

func TestLimiter_ContextCanceled(t *testing.T) {
	lim := loadshed.NewLimiter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lim.Acquire(ctx, loadshed.PriorityStandard)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestLimiter_AdaptiveAdjustment(t *testing.T) {
	lim := loadshed.NewLimiter(
		loadshed.WithInitialLimit(5.0),
		loadshed.WithMinLimit(2.0),
		loadshed.WithMaxLimit(20.0),
		loadshed.WithSmoothing(0.5),
		loadshed.WithMinRTTWindow(100*time.Millisecond),
	)

	ctx := context.Background()

	// Initial fast request to establish baseline minRTT (1ms)
	done, err := lim.Acquire(ctx, loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(1 * time.Millisecond)
	done(nil)

	initStats := lim.Stats()
	if initStats.MinRTT == 0 {
		t.Fatalf("expected non-zero minRTT")
	}

	// Simulate sudden latency inflation (e.g. 100ms) to trigger gradient decrease
	doneSlow, err := lim.Acquire(ctx, loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	doneSlow(errors.New("db timeout"))

	slowStats := lim.Stats()
	if slowStats.CurrentLimit >= initStats.CurrentLimit {
		t.Errorf("expected limit to decrease under latency and error, before: %f, after: %f",
			initStats.CurrentLimit, slowStats.CurrentLimit)
	}

	// Verify Low priority early shedding when gradient is depressed
	_, err = lim.Acquire(ctx, loadshed.PriorityLow)
	if !errors.Is(err, loadshed.ErrLoadShed) {
		t.Errorf("expected PriorityLow to be shed when latency is inflated, got %v", err)
	}
}

func TestLimiter_ReleaseIdempotence(t *testing.T) {
	lim := loadshed.NewLimiter()
	done, err := lim.Acquire(context.Background(), loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Calling done multiple times must not underflow inFlight
	done(nil)
	done(nil)

	stats := lim.Stats()
	if stats.InFlight != 0 {
		t.Errorf("expected inFlight 0, got %d", stats.InFlight)
	}
}

// ---------------------------------------------------------------------
// HTTP Middleware Tests
// ---------------------------------------------------------------------

func TestMiddleware_AllowAndShed(t *testing.T) {
	lim := loadshed.NewLimiter(
		loadshed.WithInitialLimit(1.0),
		loadshed.WithMaxLimit(1.0),
	)

	var shedCount int
	mw := loadshed.Middleware(lim,
		loadshed.WithStatusCode(http.StatusTooManyRequests),
		loadshed.WithRetryAfter("2"),
		loadshed.WithOnShed(func(r *http.Request, stats loadshed.Stats) {
			shedCount++
		}),
	)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// 1. First request succeeds
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec1.Code)
	}

	// Saturate limiter manually
	doneBlock, err := lim.Acquire(context.Background(), loadshed.PriorityStandard)
	if err != nil {
		t.Fatalf("saturate acquire: %v", err)
	}
	defer doneBlock(nil)

	// 2. Second request shed with configured status and headers
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429 Too Many Requests, got %d", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") != "2" {
		t.Errorf("expected Retry-After: 2, got %s", rec2.Header().Get("Retry-After"))
	}
	if rec2.Header().Get("X-LoadShed") != "true" {
		t.Errorf("expected X-LoadShed: true, got %s", rec2.Header().Get("X-LoadShed"))
	}
	if shedCount != 1 {
		t.Errorf("expected OnShed hook to run once, got %d", shedCount)
	}
}

func TestMiddleware_PriorityExtractionAndCustomError(t *testing.T) {
	lim := loadshed.NewLimiter(loadshed.WithInitialLimit(1.0))

	var customErrorHandled bool
	mw := loadshed.Middleware(lim,
		loadshed.WithPriorityExtractor(func(r *http.Request) loadshed.Priority {
			if r.Header.Get("X-VIP") == "true" {
				return loadshed.PriorityCritical
			}
			return loadshed.DefaultPriorityExtractor(r)
		}),
		loadshed.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, stats loadshed.Stats) {
			customErrorHandled = true
			http.Error(w, "custom_shed", http.StatusServiceUnavailable)
		}),
	)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Health check path automatically critical
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	p := loadshed.DefaultPriorityExtractor(reqHealth)
	if p != loadshed.PriorityCritical {
		t.Errorf("expected /healthz to be PriorityCritical, got %v", p)
	}

	// Low priority header
	reqLow := httptest.NewRequest(http.MethodGet, "/batch", nil)
	reqLow.Header.Set("X-Priority", "low")
	if loadshed.DefaultPriorityExtractor(reqLow) != loadshed.PriorityLow {
		t.Errorf("expected X-Priority: low to be PriorityLow")
	}

	// Trigger custom error handler
	doneBlock, _ := lim.Acquire(context.Background(), loadshed.PriorityStandard)
	defer doneBlock(nil)

	rec := httptest.NewRecorder()
	reqStandard := httptest.NewRequest(http.MethodGet, "/items", nil)
	handler.ServeHTTP(rec, reqStandard)

	if !customErrorHandled {
		t.Errorf("expected custom error handler to be invoked")
	}
}

func TestMiddleware_ServerErrorReporting(t *testing.T) {
	lim := loadshed.NewLimiter()
	mw := loadshed.Middleware(lim)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal crash", http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/crash", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------
// gRPC Interceptor Tests
// ---------------------------------------------------------------------

func TestGRPC_UnaryAndStreamInterceptors(t *testing.T) {
	lim := loadshed.NewLimiter(
		loadshed.WithInitialLimit(1.0),
		loadshed.WithMaxLimit(1.0),
	)

	unaryInterceptor := loadshed.UnaryServerInterceptor(lim,
		loadshed.WithGRPCShedCode(codes.Unavailable),
		loadshed.WithGRPCPriorityExtractor(func(ctx context.Context, fullMethod string) loadshed.Priority {
			return loadshed.DefaultGRPCPriorityExtractor(ctx, fullMethod)
		}),
	)

	// 1. Successful unary RPC
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Execute"}
	unaryHandler := func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	}

	resp, err := unaryInterceptor(context.Background(), "req", info, unaryHandler)
	if err != nil || resp != "ok" {
		t.Fatalf("unary expected ok, got resp: %v, err: %v", resp, err)
	}

	// Block limiter
	done, _ := lim.Acquire(context.Background(), loadshed.PriorityStandard)
	defer done(nil)

	// 2. Shed unary RPC
	_, err = unaryInterceptor(context.Background(), "req", info, unaryHandler)
	if err == nil {
		t.Fatalf("expected error on overloaded gRPC unary call")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		t.Errorf("expected codes.Unavailable, got %v", err)
	}

	// 3. Shed stream RPC
	streamInterceptor := loadshed.StreamServerInterceptor(lim, loadshed.WithGRPCShedCode(codes.ResourceExhausted))
	streamInfo := &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}
	mockSS := &mockServerStream{ctx: context.Background()}

	err = streamInterceptor(nil, mockSS, streamInfo, func(srv any, stream grpc.ServerStream) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected error on overloaded gRPC stream call")
	}
	stStream, _ := status.FromError(err)
	if stStream.Code() != codes.ResourceExhausted {
		t.Errorf("expected codes.ResourceExhausted, got %v", stStream.Code())
	}
}

func TestGRPC_PriorityExtraction(t *testing.T) {
	// Health method
	pHealth := loadshed.DefaultGRPCPriorityExtractor(context.Background(), "/grpc.health.v1.Health/Check")
	if pHealth != loadshed.PriorityCritical {
		t.Errorf("expected health method to be PriorityCritical, got %v", pHealth)
	}

	// Metadata priority
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-priority", "high"))
	if loadshed.DefaultGRPCPriorityExtractor(ctx, "/svc/method") != loadshed.PriorityCritical {
		t.Errorf("expected metadata high to be PriorityCritical")
	}

	ctxLow := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-priority", "low"))
	if loadshed.DefaultGRPCPriorityExtractor(ctxLow, "/svc/method") != loadshed.PriorityLow {
		t.Errorf("expected metadata low to be PriorityLow")
	}

	ctxUnknown := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-priority", "other"))
	if loadshed.DefaultGRPCPriorityExtractor(ctxUnknown, "/svc/method") != loadshed.PriorityStandard {
		t.Errorf("expected unknown metadata to be PriorityStandard")
	}
}

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}
