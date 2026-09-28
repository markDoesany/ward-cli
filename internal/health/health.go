package health

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ward/ward/internal/api"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/database"
	"github.com/ward/ward/internal/security"
)

type Result struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	DurationMS int64  `json:"duration_ms"`
}

type Report struct {
	Status  string   `json:"status"`
	Results []Result `json:"results"`
}

func Run(ctx context.Context, env config.EnvironmentConfig, timeout time.Duration, maxBytes int) Report {
	results := make([]Result, len(env.Services))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for index, service := range env.Services {
		wg.Add(1)
		go func(index int, service config.ServiceConfig) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				results[index] = Result{Name: service.Name, Type: service.Type, Status: "UNKNOWN", Message: "health check cancelled"}
				return
			}
			results[index] = check(ctx, env, service, timeout, maxBytes)
		}(index, service)
	}
	wg.Wait()
	status := aggregate(results)
	return Report{Status: status, Results: results}
}

func check(parent context.Context, env config.EnvironmentConfig, service config.ServiceConfig, timeout time.Duration, maxBytes int) Result {
	started := time.Now()
	result := Result{Name: service.Name, Type: strings.ToLower(service.Type)}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	switch result.Type {
	case "http":
		healthEnv := env
		if !service.UseAuth {
			healthEnv.API.Auth = nil
			headers := make([]config.HeaderConfig, 0, len(healthEnv.API.Headers))
			for _, header := range healthEnv.API.Headers {
				if !security.IsSensitive(header.Name) {
					headers = append(headers, header)
				}
			}
			healthEnv.API.Headers = headers
		}
		client, err := api.NewClient(ctx, healthEnv, timeout)
		if err != nil {
			result.Status, result.Message = "UNKNOWN", err.Error()
			break
		}
		query, _ := url.Parse(service.URL)
		response, err := client.Get(ctx, query.Path, query.Query(), maxBytes)
		if err != nil {
			result.Status, result.Message = "UNHEALTHY", err.Error()
			break
		}
		expected := service.ExpectedStatus
		if expected == 0 {
			expected = 200
		}
		if (response.StatusCode >= 200 && response.StatusCode < 300 && service.ExpectedStatus == 0) || response.StatusCode == expected {
			result.Status, result.Message = "HEALTHY", response.Status
		} else {
			result.Status, result.Message = "UNHEALTHY", response.Status
		}
	case "tcp":
		host := service.Host
		if service.HostEnv != "" {
			host = os.Getenv(service.HostEnv)
			if host == "" {
				result.Status, result.Message = "UNKNOWN", fmt.Sprintf("required environment variable %s is missing", service.HostEnv)
				break
			}
		}
		port := service.Port
		if service.PortEnv != "" {
			raw := os.Getenv(service.PortEnv)
			var err error
			port, err = strconv.Atoi(raw)
			if raw == "" || err != nil || port < 1 || port > 65535 {
				result.Status, result.Message = "UNKNOWN", fmt.Sprintf("environment variable %s is missing or invalid", service.PortEnv)
				break
			}
		}
		connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			result.Status, result.Message = "UNHEALTHY", err.Error()
		} else {
			result.Status, result.Message = "HEALTHY", "TCP connection established"
			connection.Close()
		}
	case "mysql":
		if env.Database == nil {
			result.Status, result.Message = "UNKNOWN", "database configuration is missing"
			break
		}
		if !env.Database.IsEnabled() {
			result.Status, result.Message = "UNKNOWN", "database profile is disabled"
			break
		}
		runner, err := database.NewRunner(ctx, *env.Database, env.Kind, timeout)
		if err != nil {
			result.Status, result.Message = "UNHEALTHY", err.Error()
		} else {
			result.Status, result.Message = "HEALTHY", "MySQL connection established"
			_ = runner.Close()
		}
	default:
		result.Status, result.Message = "UNKNOWN", fmt.Sprintf("unsupported check type %q", service.Type)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result
}

func aggregate(results []Result) string {
	if len(results) == 0 {
		return "UNKNOWN"
	}
	healthy, failed, unknown := 0, 0, 0
	for _, result := range results {
		switch result.Status {
		case "HEALTHY":
			healthy++
		case "UNHEALTHY":
			failed++
		default:
			unknown++
		}
	}
	if healthy == len(results) {
		return "HEALTHY"
	}
	if failed == len(results) {
		return "UNHEALTHY"
	}
	if unknown == len(results) {
		return "UNKNOWN"
	}
	if healthy > 0 || unknown > 0 {
		return "DEGRADED"
	}
	return "UNKNOWN"
}

func HTTPStatusCode(status string) int {
	code, _ := strconv.Atoi(strings.TrimSpace(strings.Split(status, " ")[0]))
	return code
}
