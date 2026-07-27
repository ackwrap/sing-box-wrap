package route

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
)

const (
	maxRuntimeRoutes           = 1024
	maxRuntimeLeases           = 4096
	maxRuntimeUnhealthy        = 4096
	maxRuntimeValuesPerField   = 1024
	runtimeAccessEventRingSize = 4096
)

type compiledRuntimeRouting struct {
	generation uint64
	config     adapter.RuntimeRoutingConfig
	routes     []compiledRuntimeRoute
	leases     []compiledRuntimeLease
	unhealthy  map[string]struct{}
}

type compiledRuntimeRoute struct {
	value               adapter.RuntimeRoute
	sourcePrefixes      []netip.Prefix
	destinationPrefixes []netip.Prefix
}

type compiledRuntimeLease struct {
	value        adapter.RuntimeLease
	sourcePrefix netip.Prefix
}

type runtimeAccessEventRing struct {
	access      sync.Mutex
	items       [runtimeAccessEventRingSize]adapter.RuntimeAccessEvent
	start       int
	count       int
	latest      uint64
	generation  uint64
	enabled     bool
	privacyMode string
}

type runtimeRoutingMatch struct {
	outboundTag         string
	fallbackOutboundTag string
	routeID             string
	leaseID             string
	platform            string
	kind                string
}

func emptyRuntimeRoutingConfig() adapter.RuntimeRoutingConfig {
	return adapter.RuntimeRoutingConfig{
		Routes:                  []adapter.RuntimeRoute{},
		Leases:                  []adapter.RuntimeLease{},
		UnhealthyOutbounds:      []string{},
		AccessEventsPrivacyMode: adapter.RuntimeAccessPrivacyStrict,
	}
}

func (r *Router) ReplaceRuntimeRouting(config adapter.RuntimeRoutingConfig) error {
	compiled, err := r.compileRuntimeRouting(config)
	if err != nil {
		return err
	}
	r.runtimeEvents.access.Lock()
	compiled.generation = r.runtimeEvents.generation + 1
	if compiled.generation == 0 {
		compiled.generation = 1
	}
	if !compiled.config.AccessEventsEnabled ||
		r.runtimeEvents.enabled != compiled.config.AccessEventsEnabled ||
		r.runtimeEvents.privacyMode != compiled.config.AccessEventsPrivacyMode {
		r.runtimeEvents.clearLocked()
	}
	r.runtimeEvents.generation = compiled.generation
	r.runtimeEvents.enabled = compiled.config.AccessEventsEnabled
	r.runtimeEvents.privacyMode = compiled.config.AccessEventsPrivacyMode
	r.runtimeRouting.Store(compiled)
	r.runtimeEvents.access.Unlock()
	return nil
}

func (r *Router) RuntimeRoutingSnapshot() adapter.RuntimeRoutingConfig {
	snapshot := r.runtimeRouting.Load()
	if snapshot == nil {
		return emptyRuntimeRoutingConfig()
	}
	return cloneRuntimeRoutingConfig(snapshot.config)
}

func (r *Router) RuntimeAccessEvents(after uint64, limit int) adapter.RuntimeAccessEventList {
	return r.runtimeEvents.list(after, limit)
}

func (r *Router) compileRuntimeRouting(config adapter.RuntimeRoutingConfig) (*compiledRuntimeRouting, error) {
	if len(config.Routes) > maxRuntimeRoutes {
		return nil, fmt.Errorf("too many routes: maximum is %d", maxRuntimeRoutes)
	}
	if len(config.Leases) > maxRuntimeLeases {
		return nil, fmt.Errorf("too many leases: maximum is %d", maxRuntimeLeases)
	}
	if len(config.UnhealthyOutbounds) > maxRuntimeUnhealthy {
		return nil, fmt.Errorf("too many unhealthy_outbounds: maximum is %d", maxRuntimeUnhealthy)
	}
	compiled := &compiledRuntimeRouting{
		config:    emptyRuntimeRoutingConfig(),
		routes:    make([]compiledRuntimeRoute, 0, len(config.Routes)),
		leases:    make([]compiledRuntimeLease, 0, len(config.Leases)),
		unhealthy: make(map[string]struct{}, len(config.UnhealthyOutbounds)),
	}
	privacyMode := strings.TrimSpace(config.AccessEventsPrivacyMode)
	if privacyMode == "" {
		privacyMode = adapter.RuntimeAccessPrivacyStrict
	}
	if privacyMode != adapter.RuntimeAccessPrivacyStrict && privacyMode != adapter.RuntimeAccessPrivacyBalanced {
		return nil, errors.New("access_events_privacy_mode must be strict or balanced")
	}
	compiled.config.AccessEventsEnabled = config.AccessEventsEnabled
	compiled.config.AccessEventsPrivacyMode = privacyMode
	ids := make(map[string]struct{}, len(config.Routes)+len(config.Leases))
	for index, route := range config.Routes {
		normalized, err := r.compileRuntimeRoute(route, ids)
		if err != nil {
			return nil, fmt.Errorf("route[%d]: %w", index, err)
		}
		compiled.routes = append(compiled.routes, normalized)
	}
	sort.SliceStable(compiled.routes, func(i, j int) bool {
		return compiled.routes[i].value.Priority < compiled.routes[j].value.Priority
	})
	for _, route := range compiled.routes {
		compiled.config.Routes = append(compiled.config.Routes, route.value)
	}
	for index, lease := range config.Leases {
		normalized, err := r.compileRuntimeLease(lease, ids)
		if err != nil {
			return nil, fmt.Errorf("lease[%d]: %w", index, err)
		}
		compiled.leases = append(compiled.leases, normalized)
		compiled.config.Leases = append(compiled.config.Leases, normalized.value)
	}
	for index, tag := range config.UnhealthyOutbounds {
		tag, err := r.validateRuntimeOutboundTag(tag, false)
		if err != nil {
			return nil, fmt.Errorf("unhealthy_outbounds[%d]: %w", index, err)
		}
		if _, exists := compiled.unhealthy[tag]; exists {
			return nil, fmt.Errorf("unhealthy_outbounds[%d]: duplicate outbound tag %q", index, tag)
		}
		compiled.unhealthy[tag] = struct{}{}
		compiled.config.UnhealthyOutbounds = append(compiled.config.UnhealthyOutbounds, tag)
	}
	return compiled, nil
}

func (r *Router) compileRuntimeRoute(value adapter.RuntimeRoute, ids map[string]struct{}) (compiledRuntimeRoute, error) {
	var err error
	value.ID, err = validateRuntimeRoutingID(value.ID, ids)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.Platform, err = normalizeRuntimePlatform(value.Platform)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.InboundTags, err = normalizeRuntimeStrings("inbound_tags", value.InboundTags, false)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.Domains, err = normalizeRuntimeDomains("domains", value.Domains)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.DomainSuffixes, err = normalizeRuntimeDomains("domain_suffixes", value.DomainSuffixes)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.DomainKeywords, err = normalizeRuntimeStrings("domain_keywords", value.DomainKeywords, true)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.OutboundTag, err = r.validateRuntimeOutboundTag(value.OutboundTag, false)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	value.FallbackOutboundTag, err = r.validateRuntimeOutboundTag(value.FallbackOutboundTag, true)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	var sourcePrefixes []netip.Prefix
	value.SourcePrefixes, sourcePrefixes, err = normalizeRuntimePrefixes("source_prefixes", value.SourcePrefixes)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	var destinationPrefixes []netip.Prefix
	value.DestinationPrefixes, destinationPrefixes, err = normalizeRuntimePrefixes("destination_prefixes", value.DestinationPrefixes)
	if err != nil {
		return compiledRuntimeRoute{}, err
	}
	return compiledRuntimeRoute{value: value, sourcePrefixes: sourcePrefixes, destinationPrefixes: destinationPrefixes}, nil
}

func (r *Router) compileRuntimeLease(value adapter.RuntimeLease, ids map[string]struct{}) (compiledRuntimeLease, error) {
	var err error
	value.ID, err = validateRuntimeRoutingID(value.ID, ids)
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	value.Platform, err = normalizeRuntimePlatform(value.Platform)
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	value.InboundTags, err = normalizeRuntimeStrings("inbound_tags", value.InboundTags, false)
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	value.OutboundTag, err = r.validateRuntimeOutboundTag(value.OutboundTag, false)
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	value.FallbackOutboundTag, err = r.validateRuntimeOutboundTag(value.FallbackOutboundTag, true)
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	normalizedPrefixes, prefixes, err := normalizeRuntimePrefixes("source_prefix", []string{value.SourcePrefix})
	if err != nil {
		return compiledRuntimeLease{}, err
	}
	value.SourcePrefix = normalizedPrefixes[0]
	if value.ExpiresAt < 0 {
		return compiledRuntimeLease{}, errors.New("expires_at must not be negative")
	}
	return compiledRuntimeLease{value: value, sourcePrefix: prefixes[0]}, nil
}

func (r *Router) validateRuntimeOutboundTag(tag string, optional bool) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		if optional {
			return "", nil
		}
		return "", errors.New("missing outbound_tag")
	}
	if !validRuntimeRoutingText(tag, 256) {
		return "", errors.New("invalid outbound tag")
	}
	if r.outbound == nil {
		return "", errors.New("outbound manager is unavailable")
	}
	if _, loaded := r.outbound.Outbound(tag); !loaded {
		return "", fmt.Errorf("outbound not found: %s", tag)
	}
	return tag, nil
}

func validateRuntimeRoutingID(id string, ids map[string]struct{}) (string, error) {
	id = strings.TrimSpace(id)
	if !validRuntimeRoutingText(id, 128) {
		return "", errors.New("invalid or missing id")
	}
	if _, exists := ids[id]; exists {
		return "", fmt.Errorf("duplicate id %q", id)
	}
	ids[id] = struct{}{}
	return id, nil
}

func normalizeRuntimePlatform(platform string) (string, error) {
	platform = strings.TrimSpace(platform)
	if platform != "" && !validRuntimePlatformLabel(platform) {
		return "", errors.New("invalid platform")
	}
	return platform, nil
}

func validRuntimePlatformLabel(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return false
	}
	for index, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			continue
		}
		if index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}

func normalizeRuntimeStrings(field string, values []string, lowercase bool) ([]string, error) {
	if len(values) > maxRuntimeValuesPerField {
		return nil, fmt.Errorf("too many %s values: maximum is %d", field, maxRuntimeValuesPerField)
	}
	normalized := make([]string, 0, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		if lowercase {
			value = strings.ToLower(value)
		}
		if !validRuntimeRoutingText(value, 253) {
			return nil, fmt.Errorf("%s[%d] is invalid", field, index)
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func normalizeRuntimeDomains(field string, values []string) ([]string, error) {
	normalized, err := normalizeRuntimeStrings(field, values, true)
	if err != nil {
		return nil, err
	}
	for index, domain := range normalized {
		domain = strings.TrimSuffix(domain, ".")
		if !M.IsDomainName(domain) {
			return nil, fmt.Errorf("%s[%d] is not a valid domain", field, index)
		}
		normalized[index] = domain
	}
	return normalized, nil
}

func normalizeRuntimePrefixes(field string, values []string) ([]string, []netip.Prefix, error) {
	if len(values) > maxRuntimeValuesPerField {
		return nil, nil, fmt.Errorf("too many %s values: maximum is %d", field, maxRuntimeValuesPerField)
	}
	normalized := make([]string, 0, len(values))
	prefixes := make([]netip.Prefix, 0, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil || address.Zone() != "" {
				return nil, nil, fmt.Errorf("%s[%d] is not a valid CIDR or IP", field, index)
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if prefix.Addr().Is4In6() {
			return nil, nil, fmt.Errorf("%s[%d] uses an IPv4-mapped IPv6 address", field, index)
		}
		prefix = prefix.Masked()
		normalized = append(normalized, prefix.String())
		prefixes = append(prefixes, prefix)
	}
	return normalized, prefixes, nil
}

func validRuntimeRoutingText(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func cloneRuntimeRoutingConfig(config adapter.RuntimeRoutingConfig) adapter.RuntimeRoutingConfig {
	cloned := emptyRuntimeRoutingConfig()
	cloned.AccessEventsEnabled = config.AccessEventsEnabled
	cloned.AccessEventsPrivacyMode = config.AccessEventsPrivacyMode
	cloned.UnhealthyOutbounds = append(cloned.UnhealthyOutbounds, config.UnhealthyOutbounds...)
	for _, value := range config.Routes {
		value.InboundTags = append([]string{}, value.InboundTags...)
		value.SourcePrefixes = append([]string{}, value.SourcePrefixes...)
		value.Domains = append([]string{}, value.Domains...)
		value.DomainSuffixes = append([]string{}, value.DomainSuffixes...)
		value.DomainKeywords = append([]string{}, value.DomainKeywords...)
		value.DestinationPrefixes = append([]string{}, value.DestinationPrefixes...)
		cloned.Routes = append(cloned.Routes, value)
	}
	for _, value := range config.Leases {
		value.InboundTags = append([]string{}, value.InboundTags...)
		cloned.Leases = append(cloned.Leases, value)
	}
	return cloned
}

func (r *Router) runtimeRoutingOutbound(metadata adapter.InboundContext, network string) (adapter.Outbound, bool, error) {
	return r.runtimeRoutingOutboundForSnapshot(metadata, network, r.runtimeRouting.Load())
}

func (r *Router) runtimeRoutingOutboundForSnapshot(metadata adapter.InboundContext, network string, snapshot *compiledRuntimeRouting) (adapter.Outbound, bool, error) {
	if snapshot == nil {
		return nil, false, nil
	}
	outbound, selected, err := r.runtimeLeaseOutboundForSnapshot(metadata, network, snapshot)
	if selected {
		return outbound, true, err
	}
	return r.runtimeRouteOutboundForSnapshot(metadata, network, snapshot)
}

func (r *Router) runtimeLeaseOutboundForSnapshot(metadata adapter.InboundContext, network string, snapshot *compiledRuntimeRouting) (adapter.Outbound, bool, error) {
	if snapshot == nil {
		return nil, false, nil
	}
	now := time.Now().UnixMilli()
	for _, lease := range snapshot.leases {
		if lease.value.ExpiresAt <= now || !runtimeLeaseMatches(lease, metadata) {
			continue
		}
		return r.resolveRuntimeRoutingMatch(metadata, network, snapshot, runtimeRoutingMatch{
			outboundTag: lease.value.OutboundTag, fallbackOutboundTag: lease.value.FallbackOutboundTag,
			leaseID: lease.value.ID, platform: lease.value.Platform, kind: "lease",
		})
	}
	return nil, false, nil
}

func (r *Router) runtimeRouteOutboundForSnapshot(metadata adapter.InboundContext, network string, snapshot *compiledRuntimeRouting) (adapter.Outbound, bool, error) {
	if snapshot == nil {
		return nil, false, nil
	}
	for _, route := range snapshot.routes {
		if !runtimeRouteMatches(route, metadata) {
			continue
		}
		return r.resolveRuntimeRoutingMatch(metadata, network, snapshot, runtimeRoutingMatch{
			outboundTag: route.value.OutboundTag, fallbackOutboundTag: route.value.FallbackOutboundTag,
			routeID: route.value.ID, platform: route.value.Platform, kind: "route",
		})
	}
	return nil, false, nil
}

func runtimeRoutingNeedsRuleMetadata(snapshot *compiledRuntimeRouting, metadata adapter.InboundContext) bool {
	if snapshot == nil || metadata.Domain != "" || metadata.Destination.Fqdn != "" {
		return false
	}
	for _, route := range snapshot.routes {
		value := route.value
		if len(value.Domains) == 0 && len(value.DomainSuffixes) == 0 && len(value.DomainKeywords) == 0 {
			continue
		}
		if runtimeStringDimensionMatches(value.InboundTags, metadata.Inbound) &&
			runtimePrefixDimensionMatches(route.sourcePrefixes, metadata.Source.Addr) &&
			runtimePrefixDimensionMatches(route.destinationPrefixes, runtimeDestinationAddress(metadata)) {
			return true
		}
	}
	return false
}

func runtimeLeaseMatches(lease compiledRuntimeLease, metadata adapter.InboundContext) bool {
	return runtimeStringDimensionMatches(lease.value.InboundTags, metadata.Inbound) &&
		runtimePrefixContains(lease.sourcePrefix, metadata.Source.Addr)
}

func runtimeRouteMatches(route compiledRuntimeRoute, metadata adapter.InboundContext) bool {
	domain := strings.ToLower(strings.TrimSuffix(metadata.Domain, "."))
	if domain == "" {
		domain = strings.ToLower(strings.TrimSuffix(metadata.Destination.Fqdn, "."))
	}
	return runtimeStringDimensionMatches(route.value.InboundTags, metadata.Inbound) &&
		runtimePrefixDimensionMatches(route.sourcePrefixes, metadata.Source.Addr) &&
		runtimeDomainDimensionsMatch(route.value, domain) &&
		runtimePrefixDimensionMatches(route.destinationPrefixes, runtimeDestinationAddress(metadata))
}

func runtimeStringDimensionMatches(values []string, actual string) bool {
	return len(values) == 0 || common.Contains(values, actual)
}

func runtimePrefixDimensionMatches(prefixes []netip.Prefix, address netip.Addr) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if runtimePrefixContains(prefix, address) {
			return true
		}
	}
	return false
}

func runtimePrefixContains(prefix netip.Prefix, address netip.Addr) bool {
	return address.IsValid() && prefix.Contains(address.Unmap())
}

func runtimeDomainDimensionsMatch(route adapter.RuntimeRoute, domain string) bool {
	if len(route.Domains) > 0 && (domain == "" || !common.Contains(route.Domains, domain)) {
		return false
	}
	if len(route.DomainSuffixes) > 0 {
		matched := false
		for _, suffix := range route.DomainSuffixes {
			if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(route.DomainKeywords) > 0 {
		matched := false
		for _, keyword := range route.DomainKeywords {
			if strings.Contains(domain, keyword) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (r *Router) resolveRuntimeRoutingMatch(metadata adapter.InboundContext, network string, snapshot *compiledRuntimeRouting, match runtimeRoutingMatch) (adapter.Outbound, bool, error) {
	outbound, reason := r.availableRuntimeOutbound(match.outboundTag, network, snapshot.unhealthy)
	decision := match.kind
	selectedTag := match.outboundTag
	if reason != "" {
		if match.fallbackOutboundTag != "" {
			fallback, fallbackReason := r.availableRuntimeOutbound(match.fallbackOutboundTag, network, snapshot.unhealthy)
			if fallbackReason == "" {
				outbound = fallback
				selectedTag = match.fallbackOutboundTag
				decision += "_fallback"
				reason = ""
			} else {
				reason = reason + "; fallback unavailable: " + fallbackReason
			}
		}
	}
	if reason != "" {
		decision += "_failed"
		err := errors.New(reason)
		r.recordRuntimeAccessEventForSnapshot(snapshot, metadata, network, selectedTag, match.platform, match.routeID, match.leaseID, decision, err)
		return nil, true, err
	}
	r.recordRuntimeAccessEventForSnapshot(snapshot, metadata, network, selectedTag, match.platform, match.routeID, match.leaseID, decision, nil)
	return outbound, true, nil
}

func (r *Router) availableRuntimeOutbound(tag string, network string, unhealthy map[string]struct{}) (adapter.Outbound, string) {
	if _, unavailable := unhealthy[tag]; unavailable {
		return nil, "outbound is unhealthy: " + tag
	}
	outbound, loaded := r.outbound.Outbound(tag)
	if !loaded {
		return nil, "runtime outbound not found: " + tag
	}
	if !common.Contains(outbound.Network(), network) {
		return nil, network + " is not supported by runtime outbound: " + tag
	}
	return outbound, ""
}

func (r *Router) recordRuntimeAccessEvent(metadata adapter.InboundContext, network string, outboundTag string, platform string, routeID string, leaseID string, decision string, decisionErr error) {
	r.recordRuntimeAccessEventForSnapshot(r.runtimeRouting.Load(), metadata, network, outboundTag, platform, routeID, leaseID, decision, decisionErr)
}

func (r *Router) recordRuntimeAccessEventForSnapshot(snapshot *compiledRuntimeRouting, metadata adapter.InboundContext, network string, outboundTag string, platform string, routeID string, leaseID string, decision string, decisionErr error) {
	if snapshot == nil || !snapshot.config.AccessEventsEnabled {
		return
	}
	domain := metadata.Domain
	if domain == "" {
		domain = metadata.Destination.Fqdn
	}
	event := adapter.RuntimeAccessEvent{
		Time: time.Now().UnixMilli(), Network: network, Inbound: metadata.Inbound,
		SourceIP: runtimeAddressString(metadata.Source.Addr), DestinationIP: runtimeAddressString(runtimeDestinationAddress(metadata)),
		Domain: domain, OutboundTag: outboundTag, Platform: platform, RouteID: routeID, LeaseID: leaseID, Decision: decision,
	}
	if decisionErr != nil {
		event.Error = decisionErr.Error()
	}
	r.runtimeEvents.add(event, snapshot)
}

func runtimeDestinationAddress(metadata adapter.InboundContext) netip.Addr {
	if metadata.Destination.Addr.IsValid() {
		return metadata.Destination.Addr
	}
	return metadata.OriginDestination.Addr
}

func runtimeAddressString(address netip.Addr) string {
	if !address.IsValid() {
		return ""
	}
	return address.Unmap().String()
}

func (r *runtimeAccessEventRing) add(event adapter.RuntimeAccessEvent, snapshot *compiledRuntimeRouting) {
	r.access.Lock()
	if !r.enabled || snapshot == nil || snapshot.generation != r.generation {
		r.access.Unlock()
		return
	}
	if snapshot.config.AccessEventsPrivacyMode == adapter.RuntimeAccessPrivacyStrict {
		event.SourceIP = ""
		event.DestinationIP = ""
		event.Domain = ""
		event.Platform = ""
	}
	candidate := uint64(event.Time) << 20
	if candidate <= r.latest {
		candidate = r.latest + 1
	}
	event.ID = candidate
	r.latest = candidate
	if r.count < len(r.items) {
		r.items[(r.start+r.count)%len(r.items)] = event
		r.count++
	} else {
		r.items[r.start] = event
		r.start = (r.start + 1) % len(r.items)
	}
	r.access.Unlock()
}

func (r *runtimeAccessEventRing) clearLocked() {
	r.items = [runtimeAccessEventRingSize]adapter.RuntimeAccessEvent{}
	r.start = 0
	r.count = 0
}

func (r *runtimeAccessEventRing) list(after uint64, limit int) adapter.RuntimeAccessEventList {
	result := adapter.RuntimeAccessEventList{Items: []adapter.RuntimeAccessEvent{}}
	r.access.Lock()
	result.LatestID = r.latest
	for index := 0; index < r.count && len(result.Items) < limit; index++ {
		event := r.items[(r.start+index)%len(r.items)]
		if event.ID > after {
			result.Items = append(result.Items, event)
		}
	}
	r.access.Unlock()
	return result
}
