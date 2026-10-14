package api

import (
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestEndpointSettingsAPI(t *testing.T) {
	h := setup(t)
	h.login()
	var got store.EndpointSettings
	expect(t, "get", h.do("GET", "/api/endpoint-settings", nil, &got), 200)
	if got.Retries != store.EndpointRetries || len(got.RecheckSeconds) != 2 {
		t.Fatalf("defaults = %+v", got)
	}
	expect(t, "too many", h.do("PATCH", "/api/endpoint-settings", map[string]any{"retries": 9}, nil), 422)
	expect(t, "too short", h.do("PATCH", "/api/endpoint-settings", map[string]any{"recheck_seconds": []int{1}}, nil), 422)
	expect(t, "save", h.do("PATCH", "/api/endpoint-settings", map[string]any{"retries": 3}, &got), 200)
	if got.Retries != 3 || got.RecheckSeconds[0] != 30 {
		t.Fatalf("after patch = %+v", got)
	}
	var list struct{ Endpoints []store.Endpoint }
	expect(t, "list", h.do("GET", "/api/endpoints", nil, &list), 200)
	if len(list.Endpoints) == 0 || list.Endpoints[0].RetryLimit != 3 {
		t.Fatalf("endpoints do not carry the limit: %+v", list)
	}
}
