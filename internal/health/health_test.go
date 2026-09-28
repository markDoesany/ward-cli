package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ward/ward/internal/config"
)

func TestAggregate(t *testing.T) {
	cases := []struct {
		name, want string
		results    []Result
	}{
		{name: "all healthy", want: "HEALTHY", results: []Result{{Status: "HEALTHY"}, {Status: "HEALTHY"}}},
		{name: "degraded", want: "DEGRADED", results: []Result{{Status: "HEALTHY"}, {Status: "UNHEALTHY"}}},
		{name: "all failed", want: "UNHEALTHY", results: []Result{{Status: "UNHEALTHY"}, {Status: "UNHEALTHY"}}},
		{name: "all unknown", want: "UNKNOWN", results: []Result{{Status: "UNKNOWN"}, {Status: "UNKNOWN"}}},
		{name: "none", want: "UNKNOWN", results: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := aggregate(test.results); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestRunTreatsConfiguredUnauthorizedAsHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	report := Run(context.Background(), config.EnvironmentConfig{
		API:      config.APIConfig{BaseURL: server.URL},
		Services: []config.ServiceConfig{{Name: "protected session", Type: "http", URL: "/auth/session", ExpectedStatus: http.StatusUnauthorized}},
	}, time.Second, config.DefaultMaxResponse)
	if report.Status != "HEALTHY" || len(report.Results) != 1 || report.Results[0].Status != "HEALTHY" {
		t.Fatalf("unexpected report: %#v", report)
	}
}
