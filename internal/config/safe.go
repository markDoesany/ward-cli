package config

import (
	"encoding/json"

	"github.com/ward/ward/internal/security"
)

func SafeJSON(cfg ProjectConfig) ([]byte, error) {
	copyCfg := cfg
	copyCfg.Environments = make(map[string]EnvironmentConfig, len(cfg.Environments))
	for id, env := range cfg.Environments {
		copyEnv := env
		copyEnv.API.Headers = append([]HeaderConfig(nil), env.API.Headers...)
		for i := range copyEnv.API.Headers {
			if security.IsSensitive(copyEnv.API.Headers[i].Name) && copyEnv.API.Headers[i].Value != "" {
				copyEnv.API.Headers[i].Value = security.Mask(copyEnv.API.Headers[i].Value)
			}
		}
		copyCfg.Environments[id] = copyEnv
	}
	data, err := json.Marshal(copyCfg)
	if err != nil {
		return nil, err
	}
	return security.RedactJSON(data), nil
}
