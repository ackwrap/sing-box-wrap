package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

func TestRuntimeAPIRequiresBearerSecret(t *testing.T) {
	api := &runtimeAPI{secret: "test-secret", exposures: make(map[string]runtimeNodeExposure)}
	request := httptest.NewRequest(http.MethodGet, runtimeNodeExposurePath, nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, runtimeNodeExposurePath, nil)
	request.Header.Set("Authorization", "Bearer test-secret")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected authorized status: %d", response.Code)
	}
}

func TestAPIServiceDoesNotRegisterRuntimeAPIWithoutSecret(t *testing.T) {
	service, err := NewService(context.Background(), log.NewNOPFactory().NewLogger("test"), "api", option.APIServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.(*Service).runtimeAPI != nil {
		t.Fatal("runtime API was registered without a secret")
	}
}

func TestWebBridgeReservesRuntimePathWithoutSecret(t *testing.T) {
	bridge := &webBridge{dashboard: &dashboard{}}
	request := httptest.NewRequest(http.MethodGet, runtimeNodeExposurePath, nil)
	response := httptest.NewRecorder()
	bridge.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestRuntimeAPIExposureLifecycle(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	exposure := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(exposure); err != nil {
		t.Fatal(err)
	}
	if inboundManager.items[exposure.Inbound.Tag] != exposure.Inbound.Type {
		t.Fatal("runtime inbound was not created")
	}
	if outboundManager.items[exposure.Outbound.Tag] != exposure.Outbound.Type {
		t.Fatal("runtime outbound was not created")
	}
	if runtimeRouter.items[exposure.Inbound.Tag] != exposure.Outbound.Tag {
		t.Fatal("runtime route was not created")
	}
	if err := api.delete(exposure.ID); err != nil {
		t.Fatal(err)
	}
	if len(inboundManager.items) != 0 || len(outboundManager.items) != 0 || len(runtimeRouter.items) != 0 {
		t.Fatal("runtime resources were not deleted")
	}
}

func TestRuntimeAPIUpdateRestoresPreviousExposure(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	existing := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(existing); err != nil {
		t.Fatal(err)
	}
	inboundManager.failCreateAt = 2
	replacement := testRuntimeExposure("1", "socks", "block")
	err := api.upsert(replacement)
	if err == nil {
		t.Fatal("expected replacement failure")
	}
	if inboundManager.items[existing.Inbound.Tag] != existing.Inbound.Type {
		t.Fatal("previous inbound was not restored")
	}
	if outboundManager.items[existing.Outbound.Tag] != existing.Outbound.Type {
		t.Fatal("previous outbound was not restored")
	}
	if runtimeRouter.items[existing.Inbound.Tag] != existing.Outbound.Tag {
		t.Fatal("previous runtime route was not restored")
	}
	if runtimeRouter.removeCount != 0 {
		t.Fatal("runtime route was removed during replacement")
	}
	if api.exposures[existing.ID].Inbound.Type != existing.Inbound.Type {
		t.Fatal("stored exposure changed after failed replacement")
	}
}

func TestRuntimeAPIUpdatesExposureWithoutDuplicateResources(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	existing := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(existing); err != nil {
		t.Fatal(err)
	}
	replacement := testRuntimeExposure("1", "socks", "block")
	if err := api.upsert(replacement); err != nil {
		t.Fatal(err)
	}
	if inboundManager.items[replacement.Inbound.Tag] != replacement.Inbound.Type {
		t.Fatal("replacement inbound was not created")
	}
	if outboundManager.items[replacement.Outbound.Tag] != replacement.Outbound.Type {
		t.Fatal("replacement outbound was not created")
	}
	if runtimeRouter.removeCount != 0 {
		t.Fatal("runtime route was removed during replacement")
	}
}

func TestRuntimeAPICloseCleansResourcesAndRejectsMutations(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	exposure := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(exposure); err != nil {
		t.Fatal(err)
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if len(inboundManager.items) != 0 || len(outboundManager.items) != 0 || len(runtimeRouter.items) != 0 {
		t.Fatal("runtime resources were not cleaned during close")
	}
	if err := api.upsert(testRuntimeExposure("2", "http", "direct")); !errors.Is(err, errRuntimeAPIUnavailable) {
		t.Fatalf("unexpected mutation result after close: %v", err)
	}
}

func TestRuntimeAPIRetainsFailedCleanupForDeleteRetry(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	inboundManager.failCreateAt = 1
	outboundManager.failRemove = true
	exposure := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(exposure); err == nil {
		t.Fatal("expected runtime create failure")
	}
	stored, loaded := api.exposures[exposure.ID]
	if !loaded || stored.Active {
		t.Fatal("incomplete exposure was not retained for cleanup")
	}
	if len(api.list().Items) != 0 {
		t.Fatal("incomplete exposure was returned by the list endpoint")
	}
	outboundManager.failRemove = false
	if err := api.delete(exposure.ID); err != nil {
		t.Fatal(err)
	}
	if len(outboundManager.items) != 0 || len(runtimeRouter.items) != 0 {
		t.Fatal("retry did not clean incomplete runtime resources")
	}
}

func TestRuntimeAPIRetriesIncompleteCleanupBeforePut(t *testing.T) {
	api, inboundManager, outboundManager, _ := newTestRuntimeAPI()
	inboundManager.failCreateAt = 1
	outboundManager.failRemove = true
	exposure := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(exposure); err == nil {
		t.Fatal("expected runtime create failure")
	}
	outboundManager.failRemove = false
	replacement := testRuntimeExposure("1", "socks", "block")
	if err := api.upsert(replacement); err != nil {
		t.Fatal(err)
	}
	stored := api.exposures[replacement.ID]
	if !stored.Active || stored.Inbound.Type != replacement.Inbound.Type || stored.Outbound.Type != replacement.Outbound.Type {
		t.Fatal("replacement was not created after incomplete cleanup")
	}
}

func TestRuntimeAPICloseRetainsFailedCleanupForRetry(t *testing.T) {
	api, inboundManager, outboundManager, runtimeRouter := newTestRuntimeAPI()
	exposure := testRuntimeExposure("1", "http", "direct")
	if err := api.upsert(exposure); err != nil {
		t.Fatal(err)
	}
	outboundManager.failRemove = true
	if err := api.Close(); err == nil {
		t.Fatal("expected close failure")
	}
	stored, loaded := api.exposures[exposure.ID]
	if !loaded || stored.Active {
		t.Fatal("failed close did not retain incomplete exposure")
	}
	if runtimeRouter.items[exposure.Inbound.Tag] != exposure.Outbound.Tag {
		t.Fatal("failed close removed the fail-closed runtime route")
	}
	if len(inboundManager.items) != 0 {
		t.Fatal("inbound was not removed before close failure")
	}
	outboundManager.failRemove = false
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if len(api.exposures) != 0 || len(outboundManager.items) != 0 || len(runtimeRouter.items) != 0 {
		t.Fatal("second close did not clean retained resources")
	}
}

func TestValidRuntimeExposureID(t *testing.T) {
	for _, id := range []string{"1", "node-1", "node_1", "A1"} {
		if !validRuntimeExposureID(id) {
			t.Fatalf("expected valid ID: %q", id)
		}
	}
	for _, id := range []string{"", "-node", "node/1", "node.1"} {
		if validRuntimeExposureID(id) {
			t.Fatalf("expected invalid ID: %q", id)
		}
	}
}

func newTestRuntimeAPI() (*runtimeAPI, *fakeInboundManager, *fakeOutboundManager, *fakeRuntimeRouteManager) {
	inboundManager := &fakeInboundManager{items: make(map[string]string)}
	outboundManager := &fakeOutboundManager{items: make(map[string]string)}
	runtimeRouter := &fakeRuntimeRouteManager{items: make(map[string]string)}
	return &runtimeAPI{
		ctx:             context.Background(),
		logger:          log.NewNOPFactory().NewLogger("test"),
		inboundManager:  inboundManager,
		outboundManager: outboundManager,
		runtimeRouter:   runtimeRouter,
		exposures:       make(map[string]runtimeNodeExposure),
	}, inboundManager, outboundManager, runtimeRouter
}

func testRuntimeExposure(id string, inboundType string, outboundType string) runtimeNodeExposure {
	return runtimeNodeExposure{
		ID: id,
		Inbound: option.Inbound{
			Type:    inboundType,
			Tag:     runtimeNodeExposureTagPrefix + "in-" + id,
			Options: &option.HTTPMixedInboundOptions{},
		},
		Outbound: option.Outbound{
			Type:    outboundType,
			Tag:     runtimeNodeExposureTagPrefix + "out-" + id,
			Options: &option.DirectOutboundOptions{},
		},
	}
}

type fakeInboundManager struct {
	items        map[string]string
	createCount  int
	failCreateAt int
}

func (m *fakeInboundManager) Start(adapter.StartStage) error { return nil }
func (m *fakeInboundManager) Close() error                   { return nil }
func (m *fakeInboundManager) Inbounds() []adapter.Inbound    { return nil }

func (m *fakeInboundManager) Get(tag string) (adapter.Inbound, bool) {
	_, loaded := m.items[tag]
	return nil, loaded
}

func (m *fakeInboundManager) Remove(tag string) error {
	if _, loaded := m.items[tag]; !loaded {
		return errors.New("inbound not found")
	}
	delete(m.items, tag)
	return nil
}

func (m *fakeInboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, inboundType string, _ any) error {
	m.createCount++
	if m.createCount == m.failCreateAt {
		return errors.New("inbound create failed")
	}
	m.items[tag] = inboundType
	return nil
}

type fakeOutboundManager struct {
	items      map[string]string
	failRemove bool
}

func (m *fakeOutboundManager) Start(adapter.StartStage) error { return nil }
func (m *fakeOutboundManager) Close() error                   { return nil }
func (m *fakeOutboundManager) Outbounds() []adapter.Outbound  { return nil }
func (m *fakeOutboundManager) Default() adapter.Outbound      { return nil }

func (m *fakeOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	_, loaded := m.items[tag]
	return nil, loaded
}

func (m *fakeOutboundManager) Remove(tag string) error {
	if _, loaded := m.items[tag]; !loaded {
		return errors.New("outbound not found")
	}
	if m.failRemove {
		return errors.New("outbound remove failed")
	}
	delete(m.items, tag)
	return nil
}

func (m *fakeOutboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, outboundType string, _ any) error {
	if _, loaded := m.items[tag]; loaded {
		return errors.New("duplicate outbound")
	}
	m.items[tag] = outboundType
	return nil
}

type fakeRuntimeRouteManager struct {
	items       map[string]string
	removeCount int
}

func (m *fakeRuntimeRouteManager) SetRuntimeInboundOutbound(inboundTag string, outboundTag string) {
	m.items[inboundTag] = outboundTag
}

func (m *fakeRuntimeRouteManager) RemoveRuntimeInboundOutbound(inboundTag string) {
	m.removeCount++
	delete(m.items, inboundTag)
}
