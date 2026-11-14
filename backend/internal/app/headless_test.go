package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.subcult.tv/subculture-collective/vhness/internal/community"
)

func TestHeadlessHTTPContractWithoutFrontend(t *testing.T) {
	// No web directory, frontend build or client package is needed. These public
	// discovery endpoints and collection guards work before catalog access.
	handler := (&Server{Collection: "headless-fixture", Version: "0.4.11", Revision: "fixture-revision"}).Handler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for _, path := range []string{"/", "/library", "/assets/frontend.js", "/api/unknown"} {
		if w := get(path); w.Code != http.StatusNotFound {
			t.Fatalf("headless route %s served frontend content: %d", path, w.Code)
		}
	}
	var health struct {
		Revision string `json:"revision"`
	}
	if w := get("/api/health"); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &health) != nil || health.Revision != "fixture-revision" {
		t.Fatalf("health unavailable without a frontend or missing the build revision: %d %s", w.Code, w.Body.String())
	}
	var version struct {
		API         string            `json:"api_version"`
		Core        string            `json:"core_version"`
		Revision    string            `json:"revision"`
		Collections []string          `json:"collections"`
		Modules     map[string]string `json:"modules"`
		Community   map[string]bool   `json:"community"`
	}
	w := get("/api/version")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &version) != nil || version.API != "1" || version.Core != "0.4.11" || version.Revision != "fixture-revision" {
		t.Fatalf("plain HTTP capability discovery failed: %s", w.Body.String())
	}
	for _, module := range []string{"catalog", "print", "audio", "video"} {
		if version.Modules[module] != "1" {
			t.Fatalf("missing independent media contract: %s", module)
		}
	}
	if c, ok := version.Community["personal"]; !ok || c || version.Community["chat"] {
		t.Fatalf("an engine without the community module must say accounts and chat are off: %+v", version.Community)
	}
	if len(version.Collections) != 1 || version.Collections[0] != "headless-fixture" {
		t.Fatalf("headless scope differs: %+v", version.Collections)
	}
	if w := get("/api/catalog/v1/items?collection=foreign"); w.Code != http.StatusNotFound {
		t.Fatalf("headless request escaped collection policy: %d", w.Code)
	}
}

func TestCommunityFeaturesFollowConfiguration(t *testing.T) {
	for _, c := range []struct {
		server         *Server
		personal, chat bool
	}{
		{&Server{}, false, false},
		{&Server{Community: &community.Server{}}, true, false},
		{&Server{Community: &community.Server{ChatRead: true}}, true, true},
		{&Server{Community: &community.Server{AudioChatRead: true}}, true, true},
	} {
		got := c.server.communityFeatures()
		if got["personal"] != c.personal || got["chat"] != c.chat {
			t.Fatalf("community features = %+v, want personal=%v chat=%v", got, c.personal, c.chat)
		}
	}
}
