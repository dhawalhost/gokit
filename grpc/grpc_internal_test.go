package grpc

import (
	"context"
	"net"
	"net/http"
	"testing"

	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestMetadataCarrier_Direct(t *testing.T) {
	md := metadata.New(map[string]string{
		"initial": "val",
	})
	carrier := metadataCarrier(md)

	if carrier.Get("initial") != "val" {
		t.Errorf("expected val, got %s", carrier.Get("initial"))
	}
	if carrier.Get("nonexistent") != "" {
		t.Errorf("expected empty string for missing key, got %s", carrier.Get("nonexistent"))
	}

	carrier.Set("new-key", "new-val")
	if carrier.Get("new-key") != "new-val" {
		t.Errorf("expected new-val, got %s", carrier.Get("new-key"))
	}

	keys := carrier.Keys()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestTraceContext_Helpers(t *testing.T) {
	// extractTraceContext without incoming metadata returns unchanged ctx
	ctx := context.Background()
	extracted := extractTraceContext(ctx)
	if extracted != ctx {
		t.Errorf("expected unchanged context when no incoming metadata")
	}

	// injectTraceContext with existing outgoing metadata copies and appends
	outCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("pre-existing", "true"))
	injected := injectTraceContext(outCtx)
	outMD, ok := metadata.FromOutgoingContext(injected)
	if !ok || len(outMD.Get("pre-existing")) == 0 {
		t.Error("expected pre-existing metadata preserved")
	}
}

func TestHttpStatusToGRPCCode_AllCases(t *testing.T) {
	tests := []struct {
		status   int
		expected codes.Code
	}{
		{http.StatusBadRequest, codes.InvalidArgument},
		{http.StatusUnauthorized, codes.Unauthenticated},
		{http.StatusForbidden, codes.PermissionDenied},
		{http.StatusNotFound, codes.NotFound},
		{http.StatusConflict, codes.AlreadyExists},
		{http.StatusUnprocessableEntity, codes.InvalidArgument},
		{http.StatusTooManyRequests, codes.ResourceExhausted},
		{http.StatusGatewayTimeout, codes.DeadlineExceeded},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusNotImplemented, codes.Unimplemented},
		{http.StatusTeapot, codes.Internal},
	}

	for _, tc := range tests {
		got := httpStatusToGRPCCode(tc.status)
		if got != tc.expected {
			t.Errorf("httpStatusToGRPCCode(%d) = %v; want %v", tc.status, got, tc.expected)
		}
	}
}

func TestInternalHelpers_EdgeCases(t *testing.T) {
	// splitMethodName with no slash
	service, method := splitMethodName("NoSlashMethod")
	if service != "NoSlashMethod" || method != "" {
		t.Errorf("splitMethodName unexpected: (%s, %s)", service, method)
	}

	// extractPeerIP with no peer in context
	ip := extractPeerIP(context.Background())
	if ip != "unknown" {
		t.Errorf("expected unknown, got %s", ip)
	}

	// extractPeerIP with custom peer address
	peerCtx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 50051},
	})
	ipWithPeer := extractPeerIP(peerCtx)
	if ipWithPeer != "192.168.1.50" {
		t.Errorf("expected 192.168.1.50, got %s", ipWithPeer)
	}

	// extractBearerToken edge cases
	_, err := extractBearerToken(context.Background())
	if err == nil {
		t.Error("expected error for missing metadata")
	}

	noAuthMD := metadata.NewIncomingContext(context.Background(), metadata.Pairs("other", "val"))
	_, err = extractBearerToken(noAuthMD)
	if err == nil {
		t.Error("expected error for missing authorization header")
	}

	badSchemeMD := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Basic 12345"))
	_, err = extractBearerToken(badSchemeMD)
	if err == nil {
		t.Error("expected error for invalid scheme")
	}

	emptyTokenMD := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "))
	_, err = extractBearerToken(emptyTokenMD)
	if err == nil {
		t.Error("expected error for empty token")
	}

	// UnaryClientCircuitBreaker with nil breaker passes through
	nilCbInterceptor := UnaryClientCircuitBreaker(nil)
	invoked := false
	_ = nilCbInterceptor(context.Background(), "/test/Method", nil, nil, nil, func(ctx context.Context, method string, req, reply any, cc *gogrpc.ClientConn, opts ...gogrpc.CallOption) error {
		invoked = true
		return nil
	})
	if !invoked {
		t.Error("expected invoker called with nil circuit breaker")
	}

	// UnaryLogging with nil logger passes through
	nilLogInterceptor := UnaryLogging(nil)
	invokedLog := false
	_, _ = nilLogInterceptor(context.Background(), nil, &gogrpc.UnaryServerInfo{FullMethod: "/test/Method"}, func(ctx context.Context, req any) (any, error) {
		invokedLog = true
		return nil, nil
	})
	if !invokedLog {
		t.Error("expected invoker called with nil logger")
	}

	// StreamLogging with nil logger passes through
	nilStreamLogInterceptor := StreamLogging(nil)
	invokedStreamLog := false
	_ = nilStreamLogInterceptor(nil, nil, &gogrpc.StreamServerInfo{FullMethod: "/test/Stream"}, func(srv any, stream gogrpc.ServerStream) error {
		invokedStreamLog = true
		return nil
	})
	if !invokedStreamLog {
		t.Error("expected invoker called with nil stream logger")
	}

	// UnaryClientLogging with nil logger passes through
	nilClientLogInterceptor := UnaryClientLogging(nil)
	invokedClientLog := false
	_ = nilClientLogInterceptor(context.Background(), "/test/Method", nil, nil, nil, func(ctx context.Context, method string, req, reply any, cc *gogrpc.ClientConn, opts ...gogrpc.CallOption) error {
		invokedClientLog = true
		return nil
	})
	if !invokedClientLog {
		t.Error("expected invoker called with nil client logger")
	}
}
