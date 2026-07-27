package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

func TestRuntimeRoutingAPIRequiresBearer(t *testing.T) {
	api, _, _, _ := newTestRuntimeAPI()
	api.secret = "test-secret"
	for _, path := range []string{runtimeRoutingPath, runtimeAccessEventsPath} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s: unexpected unauthenticated status %d", path, response.Code)
		}
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer test-secret")
		response = httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: unexpected authenticated status %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestRuntimeRoutingAPIStrictParsingAndAtomicFailure(t *testing.T) {
	api, _, _, runtimeRouter := newTestRuntimeAPI()
	api.secret = "test-secret"
	runtimeRouter.config = adapter.RuntimeRoutingConfig{Routes: []adapter.RuntimeRoute{{ID: "old"}}}
	replaceCalls := 0
	runtimeRouter.replace = func(adapter.RuntimeRoutingConfig) error {
		replaceCalls++
		return errors.New("validation failed")
	}

	for _, body := range []string{
		`{"routes":[],"leases":[],"unhealthy_outbounds":[],"access_events_enabled":false,"access_events_privacy_mode":"strict","unknown":true}`,
		`{"routes":[],"leases":[],"unhealthy_outbounds":[],"access_events_enabled":false,"access_events_privacy_mode":"strict"} {}`,
		`null`,
		`{}`,
		`{"routes":null,"leases":[],"unhealthy_outbounds":[],"access_events_enabled":false,"access_events_privacy_mode":"strict"}`,
		`{"routes":[],"leases":[],"unhealthy_outbounds":[]}`,
		``,
	} {
		response := serveRuntimeAPIRequest(api, http.MethodPut, runtimeRoutingPath, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected strict parse status %d for %q", response.Code, body)
		}
	}
	if replaceCalls != 0 {
		t.Fatal("invalid JSON reached the runtime router")
	}

	response := serveRuntimeAPIRequest(api, http.MethodPut, runtimeRoutingPath, `{"routes":[],"leases":[],"unhealthy_outbounds":[],"access_events_enabled":false,"access_events_privacy_mode":"strict"}`)
	if response.Code != http.StatusBadRequest || replaceCalls != 1 {
		t.Fatalf("unexpected rejected replacement result: status=%d calls=%d", response.Code, replaceCalls)
	}
	if len(runtimeRouter.config.Routes) != 1 || runtimeRouter.config.Routes[0].ID != "old" {
		t.Fatal("failed replacement changed the previous snapshot")
	}
}

func TestRuntimeRoutingAPIBodyLimit(t *testing.T) {
	api, _, _, runtimeRouter := newTestRuntimeAPI()
	api.secret = "test-secret"
	body := strings.Repeat(" ", runtimeRoutingBodyLimit+1)
	response := serveRuntimeAPIRequest(api, http.MethodPut, runtimeRoutingPath, body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected oversized body status: %d", response.Code)
	}
	if runtimeRouter.config.Routes != nil {
		t.Fatal("oversized body changed runtime routing")
	}
}

func TestRuntimeRoutingAPIPutAndGet(t *testing.T) {
	api, _, _, runtimeRouter := newTestRuntimeAPI()
	api.secret = "test-secret"
	runtimeRouter.replace = func(config adapter.RuntimeRoutingConfig) error {
		if len(config.Routes) != 1 || config.Routes[0].ID != "route-1" {
			t.Fatalf("unexpected replacement: %+v", config)
		}
		return nil
	}
	body := `{"routes":[{"id":"route-1","priority":10,"platform":"linux","inbound_tags":[],"source_prefixes":[],"domains":[],"domain_suffixes":[],"domain_keywords":[],"destination_prefixes":[],"outbound_tag":"direct","fallback_outbound_tag":""}],"leases":[],"unhealthy_outbounds":[],"access_events_enabled":true,"access_events_privacy_mode":"strict"}`
	response := serveRuntimeAPIRequest(api, http.MethodPut, runtimeRoutingPath, body)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected PUT status %d: %s", response.Code, response.Body.String())
	}
	response = serveRuntimeAPIRequest(api, http.MethodGet, runtimeRoutingPath, "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"route-1"`)) {
		t.Fatalf("unexpected GET response %d: %s", response.Code, response.Body.String())
	}
}

func TestRuntimeAccessEventsQueryValidation(t *testing.T) {
	api, _, _, runtimeRouter := newTestRuntimeAPI()
	api.secret = "test-secret"
	runtimeRouter.events = adapter.RuntimeAccessEventList{
		Items: []adapter.RuntimeAccessEvent{{ID: 12, Decision: "route"}}, LatestID: 12,
	}
	for _, query := range []string{"?limit=", "?limit=0", "?limit=501", "?after=", "?after=-1", "?after=x", "?extra=1", "?limit=1&limit=2"} {
		response := serveRuntimeAPIRequest(api, http.MethodGet, runtimeAccessEventsPath+query, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: unexpected status %d", query, response.Code)
		}
	}
	response := serveRuntimeAPIRequest(api, http.MethodGet, runtimeAccessEventsPath+"?after=11&limit=1", "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"latest_id":12`)) {
		t.Fatalf("unexpected events response %d: %s", response.Code, response.Body.String())
	}
}

func TestRuntimeRoutingAPIReturnsUnavailableAfterClose(t *testing.T) {
	api, _, _, _ := newTestRuntimeAPI()
	api.secret = "test-secret"
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{runtimeRoutingPath, runtimeAccessEventsPath} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := serveRuntimeAPIRequest(api, method, path, "")
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s %s: unexpected status after close %d", method, path, response.Code)
			}
		}
	}
}

func serveRuntimeAPIRequest(api *runtimeAPI, method string, path string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+api.secret)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
