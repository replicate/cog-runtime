package server

import "syscall"

type Status int

const (
	StatusStarting Status = iota
	StatusSetupFailed
	StatusReady
	StatusBusy
	StatusDefunct
)

func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "STARTING"
	case StatusSetupFailed:
		return "SETUP_FAILED"
	case StatusReady:
		return "READY"
	case StatusBusy:
		return "BUSY"

	case StatusDefunct:
		return "DEFUNCT"
	default:
		return "INVALID"
	}
}

type SetupStatus string

const (
	SetupSucceeded SetupStatus = "succeeded"
	SetupFailed    SetupStatus = "failed"
)

const SigOutput = syscall.SIGHUP
const SigReady = syscall.SIGUSR1
const SigBusy = syscall.SIGUSR2

type PredictionStatus string

const (
	PredictionStarting   PredictionStatus = "starting"
	PredictionProcessing PredictionStatus = "processing"
	PredictionSucceeded  PredictionStatus = "succeeded"
	PredictionCanceled   PredictionStatus = "canceled"
	PredictionFailed     PredictionStatus = "failed"
)

var PredictionCompletedStatuses = map[PredictionStatus]bool{
	PredictionSucceeded: true,
	PredictionCanceled:  true,
	PredictionFailed:    true,
}

type HealthCheck struct {
	Status string       `json:"status"`
	Setup  *SetupResult `json:"setup,omitempty"`
}

type SetupResult struct {
	StartedAt   string      `json:"started_at"`
	CompletedAt string      `json:"completed_at"`
	Status      SetupStatus `json:"status"`
	Logs        string      `json:"logs,omitempty"`
}

type PredictionRequest struct {
	Input            any    `json:"input"`
	Id               string `json:"id"`
	CreatedAt        string `json:"created_at"`
	Webhook          string `json:"webhook,omitempty"`
	OutputFilePrefix string `json:"output_file_prefix,omitempty"`
}

type PredictionResponse struct {
	Input       any              `json:"input"`
	Output      any              `json:"output"`
	Id          string           `json:"id"`
	CreatedAt   string           `json:"created_at"`
	StartedAt   string           `json:"started_at"`
	CompletedAt string           `json:"completed_at"`
	Logs        string           `json:"logs,omitempty"`
	Error       string           `json:"error,omitempty"`
	Status      PredictionStatus `json:"status,omitempty"`
}
