package api

import (
	"bytes"
	stdJSON "encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/sagernet/sing-box/adapter"
)

const (
	runtimeRoutingPath         = runtimeAPIPrefix + "runtime-routing"
	runtimeAccessEventsPath    = runtimeAPIPrefix + "access-events"
	runtimeRoutingBodyLimit    = 1 << 20
	runtimeAccessEventsLimit   = 500
	runtimeAccessEventsDefault = 100
)

func (a *runtimeAPI) handleRuntimeRouting(writer http.ResponseWriter, request *http.Request) {
	if !a.routingReady() {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	switch request.Method {
	case http.MethodGet:
		config, err := a.runtimeRoutingSnapshot()
		if err != nil {
			writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
			return
		}
		writeRuntimeAPIJSON(writer, http.StatusOK, config)
	case http.MethodPut:
		content, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, runtimeRoutingBodyLimit))
		if err != nil {
			writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "request body is too large or unreadable")
			return
		}
		config, err := decodeRuntimeRoutingConfig(content)
		if err != nil {
			writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		updated, err := a.replaceRuntimeRoutingAndSnapshot(config)
		if err != nil {
			if errors.Is(err, errRuntimeAPIUnavailable) {
				writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
				return
			}
			writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		writeRuntimeAPIJSON(writer, http.StatusOK, updated)
	default:
		writeRuntimeAPIError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (a *runtimeAPI) handleRuntimeAccessEvents(writer http.ResponseWriter, request *http.Request) {
	if !a.routingReady() {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	if request.Method != http.MethodGet {
		writeRuntimeAPIError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	after, limit, err := parseRuntimeAccessEventQuery(request)
	if err != nil {
		writeRuntimeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	events, err := a.runtimeAccessEvents(after, limit)
	if err != nil {
		writeRuntimeAPIError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", errRuntimeAPIUnavailable.Error())
		return
	}
	writeRuntimeAPIJSON(writer, http.StatusOK, events)
}

func (a *runtimeAPI) routingReady() bool {
	return !a.closing.Load() && a.outboundManager != nil && a.runtimeRouter != nil
}

func decodeRuntimeRoutingConfig(content []byte) (adapter.RuntimeRoutingConfig, error) {
	var request struct {
		Routes                  *[]adapter.RuntimeRoute `json:"routes"`
		Leases                  *[]adapter.RuntimeLease `json:"leases"`
		UnhealthyOutbounds      *[]string               `json:"unhealthy_outbounds"`
		AccessEventsEnabled     *bool                   `json:"access_events_enabled"`
		AccessEventsPrivacyMode *string                 `json:"access_events_privacy_mode"`
	}
	decoder := stdJSON.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return adapter.RuntimeRoutingConfig{}, errors.New("invalid runtime routing JSON: " + err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return adapter.RuntimeRoutingConfig{}, errors.New("invalid runtime routing JSON: multiple values")
		}
		return adapter.RuntimeRoutingConfig{}, errors.New("invalid runtime routing JSON: " + err.Error())
	}
	if request.Routes == nil || request.Leases == nil || request.UnhealthyOutbounds == nil || request.AccessEventsEnabled == nil || request.AccessEventsPrivacyMode == nil {
		return adapter.RuntimeRoutingConfig{}, errors.New("routes, leases, unhealthy_outbounds, access_events_enabled, and access_events_privacy_mode are required")
	}
	return adapter.RuntimeRoutingConfig{
		Routes:                  *request.Routes,
		Leases:                  *request.Leases,
		UnhealthyOutbounds:      *request.UnhealthyOutbounds,
		AccessEventsEnabled:     *request.AccessEventsEnabled,
		AccessEventsPrivacyMode: *request.AccessEventsPrivacyMode,
	}, nil
}

func parseRuntimeAccessEventQuery(request *http.Request) (uint64, int, error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "after" && key != "limit" {
			return 0, 0, errors.New("unknown query parameter: " + key)
		}
		if len(values) != 1 {
			return 0, 0, errors.New("query parameter must be specified once: " + key)
		}
	}
	var after uint64
	if values, exists := query["after"]; exists {
		value := values[0]
		if value == "" {
			return 0, 0, errors.New("after must be an unsigned 64-bit integer")
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, 0, errors.New("after must be an unsigned 64-bit integer")
		}
		after = parsed
	}
	limit := runtimeAccessEventsDefault
	if values, exists := query["limit"]; exists {
		value := values[0]
		if value == "" {
			return 0, 0, errors.New("limit must be between 1 and 500")
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > runtimeAccessEventsLimit {
			return 0, 0, errors.New("limit must be between 1 and 500")
		}
		limit = parsed
	}
	return after, limit, nil
}

func (a *runtimeAPI) replaceRuntimeRoutingAndSnapshot(config adapter.RuntimeRoutingConfig) (adapter.RuntimeRoutingConfig, error) {
	a.access.Lock()
	defer a.access.Unlock()
	if a.closing.Load() || a.runtimeRouter == nil {
		return adapter.RuntimeRoutingConfig{}, errRuntimeAPIUnavailable
	}
	if err := a.runtimeRouter.ReplaceRuntimeRouting(config); err != nil {
		return adapter.RuntimeRoutingConfig{}, err
	}
	return a.runtimeRouter.RuntimeRoutingSnapshot(), nil
}

func (a *runtimeAPI) runtimeRoutingSnapshot() (adapter.RuntimeRoutingConfig, error) {
	a.access.Lock()
	defer a.access.Unlock()
	if a.closing.Load() || a.runtimeRouter == nil {
		return adapter.RuntimeRoutingConfig{}, errRuntimeAPIUnavailable
	}
	return a.runtimeRouter.RuntimeRoutingSnapshot(), nil
}

func (a *runtimeAPI) runtimeAccessEvents(after uint64, limit int) (adapter.RuntimeAccessEventList, error) {
	a.access.Lock()
	defer a.access.Unlock()
	if a.closing.Load() || a.runtimeRouter == nil {
		return adapter.RuntimeAccessEventList{}, errRuntimeAPIUnavailable
	}
	return a.runtimeRouter.RuntimeAccessEvents(after, limit), nil
}
