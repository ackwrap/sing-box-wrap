package tls

import (
	"context"
	"net"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

type unexpectedDialer struct {
	t *testing.T
}

func (d unexpectedDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	d.t.Fatal("upstream dialer must not be called without a TLS config")
	return nil, nil
}

func (d unexpectedDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	d.t.Fatal("upstream dialer must not be called without a TLS config")
	return nil, nil
}

func TestDefaultDialerRejectsMissingTLSConfig(t *testing.T) {
	dialer := NewDialer(unexpectedDialer{t: t}, nil)
	if _, err := dialer.DialTLSContext(context.Background(), M.Socksaddr{Fqdn: "example.com", Port: 443}); err == nil {
		t.Fatal("DialTLSContext() error = nil, want missing TLS config error")
	}
}
