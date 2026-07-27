package inbound

import (
	"context"
	"errors"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

func TestRemoveKeepsInboundRegisteredWhenCloseFails(t *testing.T) {
	inbound := &testInbound{closeErr: errors.New("close failed")}
	manager := &Manager{
		started:      true,
		inbounds:     []adapter.Inbound{inbound},
		inboundByTag: map[string]adapter.Inbound{inbound.Tag(): inbound},
	}
	if err := manager.Remove(inbound.Tag()); err == nil {
		t.Fatal("expected close failure")
	}
	if _, loaded := manager.Get(inbound.Tag()); !loaded {
		t.Fatal("inbound was unregistered after close failure")
	}
	inbound.closeErr = nil
	if err := manager.Remove(inbound.Tag()); err != nil {
		t.Fatal(err)
	}
	if _, loaded := manager.inboundByTag[inbound.Tag()]; loaded {
		t.Fatal("inbound was not removed after successful close")
	}
}

func TestCreateClosesInboundWhenStartFails(t *testing.T) {
	inbound := &testInbound{startErr: errors.New("start failed")}
	manager := NewManager(log.NewNOPFactory().NewLogger("test"), &testInboundRegistry{inbound: inbound}, nil)
	manager.started = true
	if err := manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("test"), inbound.Tag(), inbound.Type(), nil); err == nil {
		t.Fatal("expected start failure")
	}
	if inbound.closeCount != 1 {
		t.Fatalf("unexpected close count: %d", inbound.closeCount)
	}
	if _, loaded := manager.inboundByTag[inbound.Tag()]; loaded {
		t.Fatal("failed inbound was registered")
	}
}

type testInbound struct {
	startErr   error
	closeErr   error
	closeCount int
}

func (i *testInbound) Start(adapter.StartStage) error { return i.startErr }
func (i *testInbound) Type() string                   { return "test" }
func (i *testInbound) Tag() string                    { return "test-inbound" }

func (i *testInbound) Close() error {
	i.closeCount++
	return i.closeErr
}

type testInboundRegistry struct {
	inbound adapter.Inbound
}

func (r *testInboundRegistry) OptionTypes() []string            { return []string{"test"} }
func (r *testInboundRegistry) CreateOptions(string) (any, bool) { return nil, true }
func (r *testInboundRegistry) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) (adapter.Inbound, error) {
	return r.inbound, nil
}
