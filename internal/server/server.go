package server

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync/atomic"
	"time"

	"github.com/replicate/go/httpclient"
	"github.com/replicate/go/uuid"

	"github.com/replicate/cog-runtime/internal/config"
	"github.com/replicate/cog-runtime/internal/logging"
	"github.com/replicate/cog-runtime/internal/runner"
)

const TimeLayout = "2006-01-02T15:04:05.999999-07:00"

// errAsyncPrediction is a sentinel error used to indicate that a prediction is being served asynchronously, it is not surfaced outside of server
var errAsyncPrediction = errors.New("async prediction")

type IPCStatus string

const (
	IPCStatusReady  IPCStatus = "READY"
	IPCStatusBUSY   IPCStatus = "BUSY"
	IPCStatusOutput IPCStatus = "OUTPUT"
)

type IPC struct {
	Name   string    `json:"name"`
	Pid    int       `json:"pid"`
	Status IPCStatus `json:"status"`
}

type Handler struct {
	cfg              config.Config
	startedAt        time.Time
	runnerManager    *runner.Manager
	gracefulShutdown atomic.Bool

	cwd string

	logger *logging.Logger
}

func NewHandler(ctx context.Context, cfg config.Config, baseLogger *logging.Logger) (*Handler, error) {
	runnerManager := runner.NewManager(ctx, cfg, baseLogger)

	h := &Handler{
		cfg:           cfg,
		startedAt:     time.Now(),
		runnerManager: runnerManager,
		cwd:           cfg.WorkingDirectory,
		logger:        baseLogger.Named("handler"),
	}

	return h, nil
}

// Start initializes the handler and its runner manager
func (h *Handler) Start(ctx context.Context) error {
	return h.runnerManager.Start(ctx)
}

// ActiveRunners returns active runners from the runner manager
func (h *Handler) ActiveRunners() []*runner.Runner {
	return h.runnerManager.Runners()
}

func (h *Handler) ExitCode() int {
	return h.runnerManager.ExitCode()
}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	hc, err := h.healthCheck()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		err := json.NewEncoder(w).Encode(hc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *Handler) healthCheck() (*HealthCheck, error) {
	log := h.logger.Sugar()

	if err := writeReadyFile(); err != nil {
		log.Errorw("failed to write ready file", "error", err)
		return nil, err
	}

	runnerSetupResult := h.runnerManager.SetupResult()
	concurrency := h.runnerManager.Concurrency()
	runnerStatus := h.runnerManager.Status()

	// Convert runner setup logs from []string to string
	logsStr := runnerSetupResult.Logs

	hc := HealthCheck{
		Status: runnerStatus,
		Setup: &SetupResult{
			StartedAt:   formatTime(h.startedAt),
			CompletedAt: formatTime(h.startedAt),
			Status:      runnerSetupResult.Status,
			Logs:        logsStr,
		},
		Concurrency: concurrency,
	}

	return &hc, nil
}

func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	schema, available := h.runnerManager.Schema()

	if !available {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.writeBytes(w, []byte(schema))
}

// ForceKillAll immediately force-kills all runners (for test cleanup)
func (h *Handler) ForceKillAll() {
	h.runnerManager.ForceKillAll()
}

func (h *Handler) Stop() error {
	// Set graceful shutdown flag to reject new predictions
	h.gracefulShutdown.Store(true)

	// Stop the runner manager synchronously
	log := h.logger.Sugar()
	if err := h.runnerManager.Stop(); err != nil {
		log.Errorw("failed to stop runner manager", "error", err)
		return err
	}
	return nil
}

func (h *Handler) HandleIPC(w http.ResponseWriter, r *http.Request) {
	log := h.logger.Sugar()

	// Debug: Log all incoming IPC requests
	log.Debugw("received IPC request",
		"method", r.Method,
		"url", r.URL.String(),
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent(),
	)

	var ipc IPC
	bs, err := io.ReadAll(r.Body)
	if err != nil {
		log.Errorw("failed to read IPC request body", "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(bs, &ipc); err != nil {
		log.Errorw("failed to unmarshal IPC request", "error", err, "body", string(bs))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := runner.DefaultRunnerName
	if h.cfg.UseProcedureMode {
		name = ipc.Name
	}

	log.Debugw("handling IPC for runner", "target_runner", name, "procedure_mode", h.cfg.UseProcedureMode, "status", ipc.Status, "pid", ipc.Pid, "name", ipc.Name)

	if err := h.runnerManager.HandleRunnerIPC(name, string(ipc.Status)); err != nil {
		if !errors.Is(err, runner.ErrRunnerNotFound) {
			log.Errorw("failed to handle IPC", "error", err, "name", ipc.Name, "pid", ipc.Pid, "status", ipc.Status)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !h.cfg.UseProcedureMode || ipc.Status != IPCStatusReady {
			log.Warnw("runner not found for IPC", "pid", ipc.Pid, "name", ipc.Name, "target_runner", name)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	log := h.logger.Sugar()

	// Reject new predictions during graceful shutdown
	if h.gracefulShutdown.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
		return
	}
	var req runner.PredictionRequest
	bs, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	if err := json.Unmarshal(bs, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (r.Header.Get("Prefer") == "respond-async") != (req.Webhook != "") {
		http.Error(w, "Prefer: respond-async and webhook mismatch", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if id != "" {
		if req.ID != "" && req.ID != id {
			http.Error(w, "prediction ID mismatch", http.StatusBadRequest)
			return
		}
		req.ID = id
	}
	if req.ID == "" {
		req.ID, err = PredictionID()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	var c chan PredictionResponse
	_, ok := req.Input.(map[string]any)
	if !ok {
		http.Error(w, "input is not a map[string]any", http.StatusBadRequest)
		return
	}

	if h.cfg.UseProcedureMode {
		// Although individual runners may have higher concurrency than the global max runners/concurrency
		// We still bail early if the global max has been reached
		hc, _ := h.healthCheck()
		concurrency := hc.Concurrency
		if concurrency.Current == concurrency.Max {
			http.Error(w, ErrConflict.Error(), http.StatusConflict)
			return
		}
		val, ok := req.Context["procedure_source_url"]
		if !ok {
			http.Error(w, "missing procedure_source_url in context", http.StatusBadRequest)
			return
		}
		procedureSourceURL, ok := val.(string)
		if !ok {
			http.Error(w, "procedure_source_url is not a string", http.StatusBadRequest)
			return
		}

		val, ok = req.Context["replicate_api_token"]
		if !ok {
			http.Error(w, "missing replicate_api_token in context", http.StatusBadRequest)
			return
		}

		token, ok := val.(string)
		if !ok {
			http.Error(w, "replicate_api_token is not a string", http.StatusBadRequest)
			return
		}
		if procedureSourceURL == "" || token == "" {
			http.Error(w, "empty procedure_source_url or replicate_api_token", http.StatusBadRequest)
			return
		}

		req.ProcedureSourceURL = procedureSourceURL
	}

	log.Infow("running prediction", "id", req.ID, "webhook", req.Webhook, "procedure_mode", h.cfg.UseProcedureMode)
	log.Debugw("procedure mode prediction request", "id", req.ID, "webhook", req.Webhook, "procedure_source_url", req.ProcedureSourceURL)

	var runnerResult *runner.PredictionResponse
	if req.Webhook != "" {
		// Async prediction
		err = h.runnerManager.PredictAsync(r.Context(), req)
		if err == nil {
			err = errAsyncPrediction // Signal to return 202
		}
	} else {
		// Sync prediction
		runnerResult, err = h.runnerManager.Predict(req)
		if err == nil {
			// Convert runner response to server response format
			c = make(chan PredictionResponse, 1)
			var logsStr string
			log.Debugw("runner result received", "id", runnerResult.ID, "logs_count", len(runnerResult.Logs))
			if len(runnerResult.Logs) > 0 {
				log.Debugw("joining logs", "logs", runnerResult.Logs)
				logsStr = runnerResult.Logs.String()
				log.Debugw("joined logs result", "logs_str", logsStr)
			}
			var metrics map[string]any
			if runnerResult.Metrics != nil {
				if m, ok := runnerResult.Metrics.(map[string]any); ok {
					metrics = m
				}
			}
			c <- PredictionResponse{
				ID:      runnerResult.ID,
				Status:  runnerResult.Status,
				Output:  runnerResult.Output,
				Error:   runnerResult.Error,
				Logs:    logsStr,
				Metrics: metrics,
			}
			close(c)
		}
	}

	switch {
	case errors.Is(err, errAsyncPrediction):
		// Async prediction sentinel received this explicitly means
		// we fall through and hit the `c == nil` if branch below
		break
	case errors.Is(err, ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, runner.ErrNoCapacity):
		http.Error(w, ErrConflict.Error(), http.StatusConflict)
		return
	case errors.Is(err, ErrDefunct):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case errors.Is(err, ErrExists):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, ErrSetupFailed):
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if c == nil {
		w.WriteHeader(http.StatusAccepted)
		resp := PredictionResponse{ID: req.ID, Status: "starting"}
		h.writeResponse(w, resp)
	} else {
		resp := <-c
		w.WriteHeader(http.StatusOK)
		h.writeResponse(w, resp)
	}
}

func (h *Handler) writeBytes(w http.ResponseWriter, bs []byte) {
	log := h.logger.Sugar()
	if _, err := w.Write(bs); err != nil {
		log.Errorw("failed to write response", "error", err)
	}
}

func (h *Handler) writeResponse(w http.ResponseWriter, resp PredictionResponse) {
	log := h.logger.Sugar()
	bs, err := json.Marshal(resp)
	if err != nil {
		log.Errorw("failed to marshal response", "error", err)
	}
	h.writeBytes(w, bs)
}

func SendWebhook(webhook string, pr *PredictionResponse) error {
	body, err := json.Marshal(pr)
	if err != nil {
		return fmt.Errorf("failed to marshal prediction response: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	// Only retry on completed webhooks
	client := http.DefaultClient
	if pr.Status.IsCompleted() {
		client = httpclient.ApplyRetryPolicy(http.DefaultClient)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.runnerManager.CancelPrediction(id); err != nil {
		if errors.Is(err, runner.ErrPredictionNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeReadyFile writes /var/run/cog/ready for the K8S pod readiness probe
// https://github.com/replicate/cog/blob/main/python/cog/server/probes.py
func writeReadyFile() error {
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		return nil
	}
	dir := "/var/run/cog"
	file := path.Join(dir, "ready")

	if _, err := os.Stat(file); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			return err
		}
	}

	return nil
}

func PredictionID() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	shuffle := make([]byte, uuid.Size)
	for i := 0; i < 4; i++ {
		shuffle[i], shuffle[i+4], shuffle[i+8], shuffle[i+12] = u[i+12], u[i+4], u[i], u[i+8]
	}
	encoding := base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)
	return encoding.EncodeToString(shuffle), nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}
