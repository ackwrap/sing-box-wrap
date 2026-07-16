package clashapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

type exitIPTestDialer struct {
	address string
	err     error
	calls   int
}

func (d *exitIPTestDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.address)
}

func (d *exitIPTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func TestFetchOutboundExitIPUsesProvidedDialer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("fl=test\nip=203.0.113.8\nloc=ZZ\n"))
	}))
	defer server.Close()

	dialer := &exitIPTestDialer{address: server.Listener.Addr().String()}
	ip, err := fetchOutboundExitIP(context.Background(), "http://198.51.100.1/cdn-cgi/trace", dialer, false)
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "203.0.113.8" {
		t.Fatalf("exit IP = %s", ip)
	}
	if dialer.calls != 1 {
		t.Fatalf("outbound dial count = %d, want 1", dialer.calls)
	}
}

func TestFetchOutboundExitIPDoesNotFallbackToDirect(t *testing.T) {
	dialer := &exitIPTestDialer{err: errors.New("outbound unavailable")}
	if _, err := fetchOutboundExitIP(context.Background(), "http://127.0.0.1/", dialer, false); err == nil {
		t.Fatal("expected outbound dial failure")
	}
	if dialer.calls != 1 {
		t.Fatalf("outbound dial count = %d, want 1", dialer.calls)
	}
}

func TestFetchOutboundExitIPRejectsUnexpectedAddressFamily(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ip=2001:db8::8\n"))
	}))
	defer server.Close()

	dialer := &exitIPTestDialer{address: server.Listener.Addr().String()}
	if _, err := fetchOutboundExitIP(context.Background(), "http://198.51.100.1/", dialer, false); err == nil {
		t.Fatal("expected address family error")
	}
}

func TestFetchOutboundExitIPRejectsRedirectAndOversizedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		handler http.Handler
	}{
		{
			name: "redirect",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.Redirect(writer, request, "http://192.0.2.1/", http.StatusFound)
			}),
		},
		{
			name: "oversized response",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte(strings.Repeat("x", outboundExitIPBodyLimit+1)))
			}),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(testCase.handler)
			defer server.Close()
			dialer := &exitIPTestDialer{address: server.Listener.Addr().String()}
			if _, err := fetchOutboundExitIP(context.Background(), "http://198.51.100.1/", dialer, false); err == nil {
				t.Fatal("expected response rejection")
			}
		})
	}
}

func TestGetProxyExitIPRejectsInvalidIPVersion(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/proxies/node/exit-ip?ip_version=5", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	getProxyExitIP(&Server{})(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestGetProxyExitIPRejectsArbitraryURL(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/proxies/node/exit-ip?ip_version=4&url=https://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	getProxyExitIP(&Server{})(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestGetProxyExitIPRejectsNonLoopbackClient(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/proxies/node/exit-ip?ip_version=4", nil)
	recorder := httptest.NewRecorder()
	getProxyExitIP(&Server{})(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestLoopbackExitIPRequest(t *testing.T) {
	for _, address := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		if !isLoopbackExitIPRequest(address) {
			t.Fatalf("loopback address rejected: %s", address)
		}
	}
	for _, address := range []string{"192.0.2.1:1234", "invalid"} {
		if isLoopbackExitIPRequest(address) {
			t.Fatalf("non-loopback address accepted: %s", address)
		}
	}
}

func TestOutboundExitIPTimeoutClassification(t *testing.T) {
	if !isOutboundExitIPTimeout(context.Background(), context.DeadlineExceeded) {
		t.Fatal("context deadline must be classified as timeout")
	}
	if isOutboundExitIPTimeout(context.Background(), errors.New("outbound unavailable")) {
		t.Fatal("ordinary outbound error must not be classified as timeout")
	}
}
