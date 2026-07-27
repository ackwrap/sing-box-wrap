package route

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestRuntimeRoutingLeasePrecedesPriorityRoute(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	config := adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
		Leases: []adapter.RuntimeLease{{
			ID: "lease-1", SourcePrefix: "10.0.0.0/24", InboundTags: []string{"tun-in"}, Platform: "test-platform",
			OutboundTag: "lease-out", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
		}},
		Routes: []adapter.RuntimeRoute{{
			ID: "route-1", Priority: -100, Platform: "test-platform", InboundTags: []string{"tun-in"},
			SourcePrefixes: []string{"10.0.0.0/8"}, OutboundTag: "route-out",
		}},
	}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["lease-out"] {
		t.Fatalf("lease did not take precedence: selected=%v handled=%v err=%v", selected, handled, err)
	}
	events := router.RuntimeAccessEvents(0, 10)
	if len(events.Items) != 1 || events.Items[0].LeaseID != "lease-1" || events.Items[0].Decision != "lease" || events.Items[0].Platform != "test-platform" {
		t.Fatalf("unexpected lease event: %+v", events)
	}
}

func TestRuntimeRoutingExpiredLeaseFallsThroughToRoute(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	config := adapter.RuntimeRoutingConfig{
		Leases: []adapter.RuntimeLease{{
			ID: "expired", SourcePrefix: "10.0.0.1", OutboundTag: "lease-out", ExpiresAt: time.Now().Add(-time.Second).UnixMilli(),
		}},
		Routes: []adapter.RuntimeRoute{{ID: "route-1", Priority: 1, OutboundTag: "route-out"}},
	}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("expired lease did not fall through: selected=%v handled=%v err=%v", selected, handled, err)
	}
}

func TestRuntimeRoutingStablePriorityAndMultidimensionalMatch(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	config := adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{
		{ID: "later", Priority: 20, OutboundTag: "lease-out"},
		{
			ID: "first-equal", Priority: 10, Platform: "test-platform", InboundTags: []string{"tun-in", "other"},
			SourcePrefixes: []string{"192.0.2.0/24", "10.0.0.0/8"}, Domains: []string{"api.example.com"},
			DomainSuffixes: []string{"example.com"}, DomainKeywords: []string{"api"},
			DestinationPrefixes: []string{"203.0.113.0/24"}, OutboundTag: "route-out",
		},
		{ID: "second-equal", Priority: 10, OutboundTag: "fallback-out"},
	}}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	snapshot := router.RuntimeRoutingSnapshot()
	if got := []string{snapshot.Routes[0].ID, snapshot.Routes[1].ID, snapshot.Routes[2].ID}; fmt.Sprint(got) != "[first-equal second-equal later]" {
		t.Fatalf("priority sort was not stable: %v", got)
	}
	metadata := runtimeRoutingMetadata()
	selected, handled, err := router.runtimeSelectedOutbound(metadata, N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("multidimensional route did not match: selected=%v handled=%v err=%v", selected, handled, err)
	}

	nonMatches := []adapter.InboundContext{
		func() adapter.InboundContext { value := metadata; value.Inbound = "wrong"; return value }(),
		func() adapter.InboundContext {
			value := metadata
			value.Source.Addr = netip.MustParseAddr("172.16.0.1")
			return value
		}(),
		func() adapter.InboundContext { value := metadata; value.Domain = "www.example.com"; return value }(),
		func() adapter.InboundContext {
			value := metadata
			value.Destination.Addr = netip.MustParseAddr("198.51.100.1")
			return value
		}(),
	}
	for index, value := range nonMatches {
		selected, handled, err = router.runtimeSelectedOutbound(value, N.NetworkTCP)
		if err != nil || !handled || selected != outbounds["fallback-out"] {
			t.Fatalf("dimension case %d unexpectedly matched first route: selected=%v handled=%v err=%v", index, selected, handled, err)
		}
	}
}

func TestRuntimeRoutingBusinessPlatformLabelDoesNotFilterHost(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{
		ID: "youtube-route", Platform: "youtube", Domains: []string{"video.example.com"}, OutboundTag: "route-out",
	}}, AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced}); err != nil {
		t.Fatal(err)
	}
	metadata := runtimeRoutingMetadata()
	metadata.Domain = "video.example.com"
	selected, handled, err := router.runtimeSelectedOutbound(metadata, N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("business platform label filtered a domain match: selected=%v handled=%v err=%v", selected, handled, err)
	}
	events := router.RuntimeAccessEvents(0, 1)
	if len(events.Items) != 1 || events.Items[0].Platform != "youtube" {
		t.Fatalf("route event did not preserve business platform label: %+v", events.Items)
	}
}

func TestRuntimeRoutingHealthyFallbackAndFailClosedForTCPAndUDP(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	config := adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
		Routes: []adapter.RuntimeRoute{{
			ID: "route-1", Priority: 1, OutboundTag: "tcp-only", FallbackOutboundTag: "fallback-out",
		}},
		UnhealthyOutbounds: []string{"tcp-only"},
	}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), network)
		if err != nil || !handled || selected != outbounds["fallback-out"] {
			t.Fatalf("%s fallback failed: selected=%v handled=%v err=%v", network, selected, handled, err)
		}
	}

	config.UnhealthyOutbounds = []string{"tcp-only", "fallback-out"}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	if selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkTCP); err == nil || !handled || selected != nil {
		t.Fatalf("unavailable fallback did not fail closed: selected=%v handled=%v err=%v", selected, handled, err)
	}
	events := router.RuntimeAccessEvents(0, 10)
	last := events.Items[len(events.Items)-1]
	if last.Decision != "route_failed" || last.Error == "" {
		t.Fatalf("unexpected fail-closed event: %+v", last)
	}
}

func TestRuntimeRoutingNetworkSupportUsesFallback(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	config := adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{
		ID: "route-1", OutboundTag: "tcp-only", FallbackOutboundTag: "fallback-out",
	}}}
	if err := router.ReplaceRuntimeRouting(config); err != nil {
		t.Fatal(err)
	}
	selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["tcp-only"] {
		t.Fatalf("TCP did not use primary: selected=%v handled=%v err=%v", selected, handled, err)
	}
	selected, handled, err = router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkUDP)
	if err != nil || !handled || selected != outbounds["fallback-out"] {
		t.Fatalf("UDP did not use supported fallback: selected=%v handled=%v err=%v", selected, handled, err)
	}
}

func TestRuntimeRoutingPrecedesNodeExposureAndRecordsBoth(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	router.SetRuntimeInboundOutbound("tun-in", "node-out")
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{
		ID: "route-1", OutboundTag: "route-out",
	}}, AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced}); err != nil {
		t.Fatal(err)
	}
	selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("dynamic route did not precede node exposure: selected=%v handled=%v err=%v", selected, handled, err)
	}
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
	}); err != nil {
		t.Fatal(err)
	}
	selected, handled, err = router.runtimeSelectedOutbound(runtimeRoutingMetadata(), N.NetworkUDP)
	if err != nil || !handled || selected != outbounds["node-out"] {
		t.Fatalf("node exposure mapping failed: selected=%v handled=%v err=%v", selected, handled, err)
	}
	events := router.RuntimeAccessEvents(0, 10)
	if len(events.Items) != 2 || events.Items[0].Decision != "route" || events.Items[1].Decision != "node_exposure" {
		t.Fatalf("unexpected decision events: %+v", events.Items)
	}
	if events.Items[1].Platform != "" {
		t.Fatalf("node exposure event unexpectedly contains a platform label: %+v", events.Items[1])
	}
}

func TestRuntimeRoutingUnhealthyNodeExposureFailsClosed(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	router.SetRuntimeInboundOutbound("tun-in", "node-out")
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		UnhealthyOutbounds: []string{"node-out"}, AccessEventsEnabled: true,
		AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
	}); err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		selected, handled, err := router.runtimeSelectedOutbound(runtimeRoutingMetadata(), network)
		if err == nil || !handled || selected != nil {
			t.Fatalf("%s unhealthy exposure did not fail closed: selected=%v handled=%v err=%v", network, selected, handled, err)
		}
	}
	events := router.RuntimeAccessEvents(0, 10)
	if len(events.Items) != 2 || events.Items[0].Decision != "node_exposure_failed" || events.Items[1].Decision != "node_exposure_failed" {
		t.Fatalf("unexpected unhealthy exposure events: %+v", events.Items)
	}
}

func TestRuntimeRoutingDisablesTUNPreMatchFastPath(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	router.ctx = context.Background()
	router.logger = log.NewNOPFactory().NewLogger("test")
	staticRule, err := R.NewRule(router.ctx, router.logger, option.Rule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{RuleAction: option.RuleAction{
			Action: C.RuleActionTypeReject,
			RejectOptions: option.RejectActionOptions{
				Method: C.RuleActionRejectMethodDefault,
			},
		}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	router.rules = []adapter.Rule{staticRule}
	metadata := runtimeRoutingMetadata()
	metadata.Network = N.NetworkTCP
	metadata.InboundType = C.TypeTun
	metadata.Destination = M.Socksaddr{Fqdn: metadata.Domain, Port: 443}
	if result := router.PreMatch(metadata, nil); result.Action != adapter.PreMatchReject {
		t.Fatalf("static rule did not establish pre-match behavior: %+v", result)
	}
	if err = router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{
		ID: "route-1", OutboundTag: "route-out",
	}}}); err != nil {
		t.Fatal(err)
	}
	if result := router.PreMatch(metadata, nil); result.Action != adapter.PreMatchContinue {
		t.Fatalf("dynamic route was bypassed by pre-match: %+v", result)
	}
	if err = router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{}); err != nil {
		t.Fatal(err)
	}
	router.SetRuntimeInboundOutbound("tun-in", "node-out")
	if result := router.PreMatch(metadata, nil); result.Action != adapter.PreMatchContinue {
		t.Fatalf("node exposure was bypassed by pre-match: %+v", result)
	}
}

func TestRuntimeRoutingUsesSniffedDomainBeforeLowerPriorityRoute(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	router.ctx = context.Background()
	router.logger = log.NewNOPFactory().NewLogger("test")
	for _, action := range []option.RuleAction{
		{Action: C.RuleActionTypeSniff},
		{Action: C.RuleActionTypeReject, RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault}},
	} {
		staticRule, err := R.NewRule(router.ctx, router.logger, option.Rule{
			Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RuleAction: action},
		}, false)
		if err != nil {
			t.Fatal(err)
		}
		router.rules = append(router.rules, staticRule)
	}
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{
		{ID: "domain", Priority: 1, Domains: []string{"api.example.com"}, OutboundTag: "route-out"},
		{ID: "catch-all", Priority: 2, OutboundTag: "fallback-out"},
	}}); err != nil {
		t.Fatal(err)
	}
	metadata := runtimeRoutingMetadata()
	metadata.Network = N.NetworkTCP
	metadata.Domain = ""
	metadata.Destination.Port = 80
	snapshot := router.runtimeRouting.Load()
	if !runtimeRoutingNeedsRuleMetadata(snapshot, metadata) {
		t.Fatal("missing domain did not request static sniff enrichment")
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		_, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: api.example.com\r\n\r\n"))
	}()
	selectedRule, _, buffers, _, err := router.matchPreparedRule(router.ctx, &metadata, server, nil)
	defer buf.ReleaseMulti(buffers)
	if err != nil {
		t.Fatal(err)
	}
	if selectedRule == nil || metadata.Domain != "api.example.com" {
		t.Fatalf("static sniff did not enrich domain before terminal rule: domain=%q rule=%v", metadata.Domain, selectedRule)
	}
	selected, handled, err := router.runtimeRouteOutboundForSnapshot(metadata, N.NetworkTCP, snapshot)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("sniffed domain did not select higher priority dynamic route: selected=%v handled=%v err=%v", selected, handled, err)
	}
}

func TestRuntimeRoutingUsesOriginalDestinationAfterDomainMapping(t *testing.T) {
	router, outbounds := newRuntimeRoutingTestRouter()
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{
		ID: "mapped", Domains: []string{"api.example.com"}, DestinationPrefixes: []string{"203.0.113.0/24"}, OutboundTag: "route-out",
	}}, AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced}); err != nil {
		t.Fatal(err)
	}
	metadata := runtimeRoutingMetadata()
	metadata.OriginDestination = metadata.Destination
	metadata.Destination = M.Socksaddr{Fqdn: metadata.Domain, Port: metadata.Destination.Port}
	selected, handled, err := router.runtimeSelectedOutbound(metadata, N.NetworkTCP)
	if err != nil || !handled || selected != outbounds["route-out"] {
		t.Fatalf("mapped destination did not match original IP: selected=%v handled=%v err=%v", selected, handled, err)
	}
	events := router.RuntimeAccessEvents(0, 1)
	if len(events.Items) != 1 || events.Items[0].DestinationIP != "203.0.113.8" {
		t.Fatalf("event lost original destination IP: %+v", events.Items)
	}
}

func TestRuntimeRoutingValidationIsAtomicAndLimited(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	old := adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{ID: "old", OutboundTag: "route-out"}}}
	if err := router.ReplaceRuntimeRouting(old); err != nil {
		t.Fatal(err)
	}
	invalid := []adapter.RuntimeRoutingConfig{
		{Routes: []adapter.RuntimeRoute{{ID: "duplicate", OutboundTag: "route-out"}}, Leases: []adapter.RuntimeLease{{ID: "duplicate", SourcePrefix: "10.0.0.0/24", OutboundTag: "lease-out"}}},
		{Routes: []adapter.RuntimeRoute{{ID: "bad-cidr", SourcePrefixes: []string{"not-an-ip"}, OutboundTag: "route-out"}}},
		{Routes: []adapter.RuntimeRoute{{ID: "bad-domain", Domains: []string{"bad domain"}, OutboundTag: "route-out"}}},
		{Routes: []adapter.RuntimeRoute{{ID: "missing-out", OutboundTag: "missing"}}},
		{Routes: []adapter.RuntimeRoute{{ID: "unsafe-platform", Platform: "https://secret.example", OutboundTag: "route-out"}}},
		{AccessEventsPrivacyMode: "unsupported"},
	}
	tooMany := adapter.RuntimeRoutingConfig{Routes: make([]adapter.RuntimeRoute, maxRuntimeRoutes+1)}
	invalid = append(invalid, tooMany)
	for index, config := range invalid {
		if err := router.ReplaceRuntimeRouting(config); err == nil {
			t.Fatalf("invalid config %d was accepted", index)
		}
		snapshot := router.RuntimeRoutingSnapshot()
		if len(snapshot.Routes) != 1 || snapshot.Routes[0].ID != "old" {
			t.Fatalf("invalid config %d changed snapshot: %+v", index, snapshot)
		}
	}
}

func TestRuntimeAccessEventRingIsBoundedOrderedAndNonDestructive(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
	}); err != nil {
		t.Fatal(err)
	}
	metadata := runtimeRoutingMetadata()
	for index := 0; index < runtimeAccessEventRingSize+4; index++ {
		router.recordRuntimeAccessEvent(metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	}
	all := router.RuntimeAccessEvents(0, runtimeAccessEventRingSize)
	if len(all.Items) != runtimeAccessEventRingSize || all.LatestID != all.Items[len(all.Items)-1].ID {
		t.Fatalf("unexpected ring size/latest: items=%d latest=%d", len(all.Items), all.LatestID)
	}
	for index := 1; index < len(all.Items); index++ {
		if all.Items[index-1].ID >= all.Items[index].ID {
			t.Fatalf("event IDs are not increasing at %d", index)
		}
	}
	first := all.Items[0]
	if first.SourceIP != "10.0.0.1" || first.DestinationIP != "203.0.113.8" || first.Domain != "api.example.com" || first.Platform != "test-platform" {
		t.Fatalf("unexpected structured event: %+v", first)
	}
	page := router.RuntimeAccessEvents(first.ID, 2)
	if len(page.Items) != 2 || page.Items[0].ID <= first.ID {
		t.Fatalf("unexpected event page: %+v", page)
	}
	again := router.RuntimeAccessEvents(0, 1)
	if len(again.Items) != 1 || again.Items[0].ID != first.ID {
		t.Fatal("reading events cleared or reordered the ring")
	}
}

func TestRuntimeAccessEventsAreOptInAndRedactedBeforeRead(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	metadata := runtimeRoutingMetadata()
	router.recordRuntimeAccessEvent(metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	if events := router.RuntimeAccessEvents(0, 10); len(events.Items) != 0 {
		t.Fatalf("disabled access events were collected: %+v", events.Items)
	}
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyStrict,
	}); err != nil {
		t.Fatal(err)
	}
	router.recordRuntimeAccessEvent(metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	strict := router.RuntimeAccessEvents(0, 10)
	if len(strict.Items) != 1 || strict.Items[0].SourceIP != "" || strict.Items[0].DestinationIP != "" || strict.Items[0].Domain != "" || strict.Items[0].Platform != "" {
		t.Fatalf("strict event retained endpoint metadata: %+v", strict.Items)
	}
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
	}); err != nil {
		t.Fatal(err)
	}
	if events := router.RuntimeAccessEvents(0, 10); len(events.Items) != 0 {
		t.Fatalf("privacy transition retained prior events: %+v", events.Items)
	}
	router.recordRuntimeAccessEvent(metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	balanced := router.RuntimeAccessEvents(0, 10)
	if len(balanced.Items) != 1 || balanced.Items[0].SourceIP == "" || balanced.Items[0].DestinationIP == "" || balanced.Items[0].Domain == "" {
		t.Fatalf("balanced event lost endpoint metadata: %+v", balanced.Items)
	}
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyStrict}); err != nil {
		t.Fatal(err)
	}
	if events := router.RuntimeAccessEvents(0, 10); len(events.Items) != 0 {
		t.Fatalf("disabling access events did not clear the ring: %+v", events.Items)
	}
}

func TestRuntimeAccessEventsRejectStaleRoutingGeneration(t *testing.T) {
	router, _ := newRuntimeRoutingTestRouter()
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyStrict,
	}); err != nil {
		t.Fatal(err)
	}
	stale := router.runtimeRouting.Load()
	if err := router.ReplaceRuntimeRouting(adapter.RuntimeRoutingConfig{
		AccessEventsEnabled: true, AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyBalanced,
	}); err != nil {
		t.Fatal(err)
	}
	metadata := runtimeRoutingMetadata()
	router.recordRuntimeAccessEventForSnapshot(stale, metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	if events := router.RuntimeAccessEvents(0, 10); len(events.Items) != 0 {
		t.Fatalf("stale generation event entered the new ring: %+v", events.Items)
	}
	router.recordRuntimeAccessEvent(metadata, N.NetworkTCP, "route-out", "test-platform", "route-1", "", "route", nil)
	events := router.RuntimeAccessEvents(0, 10)
	if len(events.Items) != 1 || events.Items[0].SourceIP == "" || events.Items[0].Platform != "test-platform" {
		t.Fatalf("current generation event was not recorded with balanced privacy: %+v", events.Items)
	}
}

func newRuntimeRoutingTestRouter() (*Router, map[string]adapter.Outbound) {
	outbounds := map[string]adapter.Outbound{
		"lease-out":    &testRuntimeOutbound{tag: "lease-out", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		"route-out":    &testRuntimeOutbound{tag: "route-out", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		"fallback-out": &testRuntimeOutbound{tag: "fallback-out", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		"node-out":     &testRuntimeOutbound{tag: "node-out", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		"tcp-only":     &testRuntimeOutbound{tag: "tcp-only", networks: []string{N.NetworkTCP}},
	}
	return &Router{
		outbound:         &testRuntimeOutboundManager{items: outbounds},
		runtimeOutbounds: make(map[string]string),
	}, outbounds
}

func runtimeRoutingMetadata() adapter.InboundContext {
	return adapter.InboundContext{
		Inbound: "tun-in",
		Source: M.Socksaddr{
			Addr: netip.MustParseAddr("10.0.0.1"), Port: 12345,
		},
		Destination: M.Socksaddr{
			Addr: netip.MustParseAddr("203.0.113.8"), Port: 443,
		},
		Domain: "api.example.com",
	}
}
