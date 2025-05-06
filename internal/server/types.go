package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"syscall"
)

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

type Config struct {
	UseProcedureMode      bool
	AwaitExplicitShutdown bool
	UploadUrl             string
}

type PredictConfig struct {
	ModuleName    string `json:"module_name,omitempty"`
	PredictorName string `json:"predictor_name,omitempty"`
}

type PredictionStatus string

const (
	PredictionStarting   PredictionStatus = "starting"
	PredictionProcessing PredictionStatus = "processing"
	PredictionSucceeded  PredictionStatus = "succeeded"
	PredictionCanceled   PredictionStatus = "canceled"
	PredictionFailed     PredictionStatus = "failed"
)

func (s PredictionStatus) IsCompleted() bool {
	return s == PredictionSucceeded || s == PredictionCanceled || s == PredictionFailed
}

type WebhookEvent string

const (
	WebhookStart     WebhookEvent = "start"
	WebhookOutput    WebhookEvent = "output"
	WebhookLogs      WebhookEvent = "logs"
	WebhookCompleted WebhookEvent = "completed"
)

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

type ProcedureRequest struct {
	ProcedureSourceURL string `json:"procedure_source_url"`
	Token              string `json:"token"`
	InputsJson         any    `json:"inputs_json"`
}

func ParseProcedureRequest(v any) (ProcedureRequest, error) {
	r := ProcedureRequest{}
	m, ok := v.(map[string]any)
	if !ok {
		return r, fmt.Errorf("invalid procedure request %v", v)
	}
	if v, ok := m["procedure_source_url"]; ok {
		vs := v.(string)
		u, err := url.Parse(vs)
		if err != nil {
			return r, err
		}
		if u.Scheme != "file" {
			return r, fmt.Errorf("invalid procedure_source_url %s", vs)
		}
		r.ProcedureSourceURL = u.Path
	}
	if v, ok := m["token"]; ok {
		r.Token = v.(string)
	}
	if v, ok := m["inputs_json"]; ok {
		vs, ok := v.(string)
		if !ok {
			return r, fmt.Errorf("invalid inputs_json %v", v)
		}
		if err := json.Unmarshal([]byte(vs), &r.InputsJson); err != nil {
			return r, err
		}
	}
	return r, nil
}

type PredictionRequest struct {
	Input               any            `json:"input"`
	Id                  string         `json:"id"`
	CreatedAt           string         `json:"created_at"`
	Webhook             string         `json:"webhook,omitempty"`
	WebhookEventsFilter []WebhookEvent `json:"webhook_events_filter,omitempty"`
	OutputFilePrefix    string         `json:"output_file_prefix,omitempty"`
}

type PredictionResponse struct {
	Input       any              `json:"input"`
	Output      any              `json:"output,omitempty"`
	Id          string           `json:"id"`
	CreatedAt   string           `json:"created_at,omitempty"`
	StartedAt   string           `json:"started_at,omitempty"`
	CompletedAt string           `json:"completed_at,omitempty"`
	Logs        string           `json:"logs,omitempty"`
	Error       string           `json:"error,omitempty"`
	Status      PredictionStatus `json:"status,omitempty"`
	Metrics     map[string]any   `json:"metrics,omitempty"`
}
