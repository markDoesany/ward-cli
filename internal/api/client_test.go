package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ward/ward/internal/config"
)

func TestGetPreservesAndAddsQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users" || r.URL.Query().Get("from") != "path" || r.URL.Query().Get("to") != "flag" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"not returned"}`))
	}))
	defer server.Close()
	client, err := NewClient(context.Background(), config.EnvironmentConfig{API: config.APIConfig{BaseURL: server.URL}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(context.Background(), "/api/users?from=path", url.Values{"to": {"flag"}}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Body, `"token": "************"`) {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestGetBlocksCrossOriginRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("unexpected")) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	client, err := NewClient(context.Background(), config.EnvironmentConfig{API: config.APIConfig{BaseURL: source.URL}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "/redirect", nil, 1024); err == nil || !strings.Contains(err.Error(), "outside configured API origin") {
		t.Fatalf("expected blocked redirect, got %v", err)
	}
}
