package server

import (
	"github.com/replicate/cog-runtime/internal/runner"
)

type PredictConfig struct {
	ModuleName     string `json:"module_name,omitempty"`
	PredictorName  string `json:"predictor_name,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type HealthCheck struct {
	Status      string             `json:"status"`
	Setup       *SetupResult       `json:"setup,omitempty"`
	Concurrency runner.Concurrency `json:"concurrency,omitempty"`
}

type SetupResult struct {
	StartedAt   string             `json:"started_at"`
	CompletedAt string             `json:"completed_at"`
	Status      runner.SetupStatus `json:"status"`
	Logs        string             `json:"logs,omitempty"`
}

type PredictionResponse struct {
	Input       any                     `json:"input"`
	Output      any                     `json:"output,omitempty"`
	ID          string                  `json:"id"`
	CreatedAt   string                  `json:"created_at,omitempty"`
	StartedAt   string                  `json:"started_at,omitempty"`
	CompletedAt string                  `json:"completed_at,omitempty"`
	Logs        string                  `json:"logs,omitempty"`
	Error       string                  `json:"error,omitempty"`
	Status      runner.PredictionStatus `json:"status,omitempty"`
	Metrics     map[string]any          `json:"metrics,omitempty"`
}
