package util //nolint:revive // FIXME: break up util package and move functions to where they're used

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
)

type MetricsPayload struct {
	Source string         `json:"source,omitempty"`
	Type   string         `json:"type,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

const MetricsEndpointEnv = "COG_METRICS_ENDPOINT"

func SendRunnerMetric(yaml CogYaml) {
	log := logger.Sugar()
	endpoint := os.Getenv(MetricsEndpointEnv)
	if endpoint == "" {
		return
	}
	data := map[string]any{
		"gpu":         yaml.Build.GPU,
		"fast":        yaml.Build.Fast,
		"cog_runtime": yaml.Build.CogRuntime,
		"version":     Version(),
	}
	payload := MetricsPayload{
		Source: "cog-runtime",
		Type:   "runner",
		Data:   data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Errorw("failed to marshal payload", "error", err)
		return
	}
	resp, err := HTTPClientWithRetry().Post(endpoint, "application/json", bytes.NewBuffer(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Errorw("failed to send runner metrics", "error", err)
	}
	defer resp.Body.Close()
}
