package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/security"
)

type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Headers    map[string]string
	Token      string
	Redactor   *security.Redactor
}

type Response struct {
	StatusCode  int               `json:"status_code"`
	Status      string            `json:"status"`
	DurationMS  int64             `json:"duration_ms"`
	ContentType string            `json:"content_type,omitempty"`
	Body        string            `json:"body"`
	Truncated   bool              `json:"truncated,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func NewClient(_ context.Context, env config.EnvironmentConfig, timeout time.Duration) (*Client, error) {
	baseURL, err := config.ResolveAPIBaseURL(env.API)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	redactor, err := security.NewRedactor(env.Redaction.Fields, env.Redaction.Patterns)
	if err != nil {
		return nil, err
	}
	if env.API.CACertPath != "" {
		data, err := os.ReadFile(env.API.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("read API CA bundle: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("API CA bundle contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	headers := make(map[string]string)
	for _, header := range env.API.Headers {
		value := header.Value
		if header.ValueEnv != "" {
			value = os.Getenv(header.ValueEnv)
			if value == "" {
				return nil, fmt.Errorf("required environment variable %s is missing", header.ValueEnv)
			}
		}
		if header.Name == "" {
			return nil, fmt.Errorf("API header name cannot be empty")
		}
		headers[header.Name] = value
	}
	token := ""
	if env.API.Auth != nil && env.API.Auth.BearerTokenEnv != "" {
		token = os.Getenv(env.API.Auth.BearerTokenEnv)
		if token == "" {
			return nil, fmt.Errorf("required environment variable %s is missing", env.API.Auth.BearerTokenEnv)
		}
	}
	origin, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse API base URL: %w", err)
	}
	if origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, fmt.Errorf("API base URL must use http or https and must not contain credentials or a query")
	}
	httpClient := &http.Client{Timeout: timeout, Transport: transport}
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !strings.EqualFold(request.URL.Scheme, origin.Scheme) || !strings.EqualFold(request.URL.Host, origin.Host) {
			return fmt.Errorf("redirect outside configured API origin is blocked")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	return &Client{HTTPClient: httpClient, BaseURL: baseURL, Headers: headers, Token: token, Redactor: redactor}, nil
}

func (c *Client) Get(ctx context.Context, relativePath string, query url.Values, maxBytes int) (Response, error) {
	if maxBytes <= 0 {
		maxBytes = config.DefaultMaxResponse
	}
	if relativePath == "" || !strings.HasPrefix(relativePath, "/") {
		return Response{}, fmt.Errorf("API path must be relative and start with /")
	}
	parsed, err := url.Parse(relativePath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return Response{}, fmt.Errorf("API path must be relative to the configured base URL")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return Response{}, fmt.Errorf("parse API base URL: %w", err)
	}
	base.Path = path.Join(base.Path, parsed.Path)
	mergedQuery := parsed.Query()
	for key, values := range query {
		for _, value := range values {
			mergedQuery.Add(key, value)
		}
	}
	base.RawQuery = mergedQuery.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return Response{}, fmt.Errorf("create API request: %w", err)
	}
	for key, value := range c.Headers {
		request.Header.Set(key, value)
	}
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	started := time.Now()
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, int64(maxBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return Response{}, fmt.Errorf("read API response: %w", err)
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	contentType := response.Header.Get("Content-Type")
	redactor := c.Redactor
	if redactor == nil {
		redactor = security.DefaultRedactor()
	}
	return Response{
		StatusCode:  response.StatusCode,
		Status:      response.Status,
		DurationMS:  time.Since(started).Milliseconds(),
		ContentType: contentType,
		Body:        string(redactor.RedactJSON(body)),
		Truncated:   truncated,
		Headers:     redactor.RedactHeaders(map[string]string{"Content-Type": contentType}),
	}, nil
}
