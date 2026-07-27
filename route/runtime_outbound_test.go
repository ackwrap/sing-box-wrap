package route

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestRuntimeInboundOutboundLifecycle(t *testing.T) {
	router := &Router{runtimeOutbounds: make(map[string]string)}
	if _, loaded := router.runtimeInboundOutbound("in"); loaded {
		t.Fatal("unexpected runtime route before registration")
	}
	router.SetRuntimeInboundOutbound("in", "out")
	if outboundTag, loaded := router.runtimeInboundOutbound("in"); !loaded || outboundTag != "out" {
		t.Fatalf("unexpected runtime route: %q, %v", outboundTag, loaded)
	}
	router.RemoveRuntimeInboundOutbound("in")
	if _, loaded := router.runtimeInboundOutbound("in"); loaded {
		t.Fatal("runtime route was not removed")
	}
}

func TestRuntimeOutboundFailsClosed(t *testing.T) {
	outbound := &testRuntimeOutbound{tag: "out", networks: []string{N.NetworkTCP}}
	manager := &testRuntimeOutboundManager{items: map[string]adapter.Outbound{"out": outbound}}
	router := &Router{outbound: manager, runtimeOutbounds: map[string]string{"in": "out"}}
	selected, runtimeRoute, err := router.runtimeOutbound("in", N.NetworkTCP)
	if err != nil || !runtimeRoute || selected != outbound {
		t.Fatalf("unexpected runtime selection: %v, %v, %v", selected, runtimeRoute, err)
	}
	if _, runtimeRoute, err = router.runtimeOutbound("in", N.NetworkUDP); err == nil || !runtimeRoute {
		t.Fatalf("unsupported network did not fail closed: %v, %v", runtimeRoute, err)
	}
	delete(manager.items, "out")
	if _, runtimeRoute, err = router.runtimeOutbound("in", N.NetworkTCP); err == nil || !runtimeRoute {
		t.Fatalf("missing outbound did not fail closed: %v, %v", runtimeRoute, err)
	}
}

type testRuntimeOutboundManager struct {
	items map[string]adapter.Outbound
}

func (m *testRuntimeOutboundManager) Start(adapter.StartStage) error { return nil }
func (m *testRuntimeOutboundManager) Close() error                   { return nil }
func (m *testRuntimeOutboundManager) Outbounds() []adapter.Outbound  { return nil }
func (m *testRuntimeOutboundManager) Default() adapter.Outbound      { return nil }

func (m *testRuntimeOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.items[tag]
	return outbound, loaded
}

func (m *testRuntimeOutboundManager) Remove(tag string) error {
	delete(m.items, tag)
	return nil
}

func (m *testRuntimeOutboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, _ string, _ string, _ any) error {
	return nil
}

type testRuntimeOutbound struct {
	tag      string
	networks []string
}

func (o *testRuntimeOutbound) Start(adapter.StartStage) error { return nil }
func (o *testRuntimeOutbound) Close() error                   { return nil }
func (o *testRuntimeOutbound) Type() string                   { return "test" }
func (o *testRuntimeOutbound) Tag() string                    { return o.tag }
func (o *testRuntimeOutbound) Network() []string              { return o.networks }
func (o *testRuntimeOutbound) Dependencies() []string         { return nil }

func (o *testRuntimeOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (o *testRuntimeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}
