package config

import (
	"fmt"
	"os"
	"strings"
)

// ResolveAPIBaseURL resolves a static URL or an environment-backed URL.
// Secrets are not involved; this is intended for local/dev endpoint selection.
func ResolveAPIBaseURL(api APIConfig) (string, error) {
	if api.BaseURL != "" && api.BaseURLEnv != "" {
		return "", fmt.Errorf("API must set only one of base_url or base_url_env")
	}
	value := api.BaseURL
	if api.BaseURLEnv != "" {
		value = os.Getenv(api.BaseURLEnv)
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("required environment variable %s is missing", api.BaseURLEnv)
		}
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("API base_url is not configured")
	}
	return strings.TrimRight(value, "/"), nil
}
