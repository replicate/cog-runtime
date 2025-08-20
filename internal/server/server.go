package server

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/replicate/cog-runtime/internal/util"
)

var logger = util.CreateLogger("cog-http-server")

//go:embed openapi-procedure.json
var procedureSchema string

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
	cfg       Config
	shutdown  context.CancelFunc
	startedAt time.Time
	setUID    bool
	runners   []*Runner
	mu        sync.Mutex

	uidCounter *uidCounter

	cwd string
}

func NewHandler(cfg Config, shutdown context.CancelFunc) (*Handler, error) {
	log := logger.Sugar()
	h := &Handler{
		cfg:        cfg,
		shutdown:   shutdown,
		startedAt:  time.Now(),
		uidCounter: &uidCounter{},
		cwd:        cfg.WorkingDirectory,
	}
	if cfg.UseProcedureMode {
		// Allow the caller to specify the max number of runners to allow. By default,
		// we will use the number of CPUs * 4. Note that NumCPU() is processor affinity aware
		// and will adhere to container resource allocations
		// FIXME: this should not be here, it should be lifted to main.go and passed to NewHandler and `0`
		// should be rejected as invalid
		maxRunners := cfg.MaxRunners
		if maxRunners == 0 {
			maxRunners = runtime.NumCPU() * 4
		}
		h.runners = make([]*Runner, maxRunners)

		_, err := os.Stat("/.dockerenv")
		inDocker := err == nil
		_, inK8S := os.LookupEnv("KUBERNETES_SERVICE_HOST")
		// Running as root inside Docker or K8S
		// Set UID to an unprivileged user in each Python runner for best-effort sandboxing
		// Each Python runner has write access to
		// * PWD, i.e., copied procedure source code
		// * Working directory, i.e., for input/output JSON files
		// * TMPDIR, for mktemp, tempfile, etc.
		if (inDocker || inK8S) && os.Getuid() == 0 {
			h.setUID = true
		}
		log.Infow("running in procedure mode", "max_runners", maxRunners)
	} else {
		h.runners = make([]*Runner, 1)
		runner, err := NewRunner(DefaultRunnerName, h.cwd, cfg)
		if err != nil {
			return nil, err
		}
		h.runners[DefaultRunnerId] = runner
		if err := h.runners[DefaultRunnerId].Start(); err != nil {
			return nil, err
		}

		if !cfg.AwaitExplicitShutdown {
			go func() {
				// Shut down as soon as runner exists
				h.runners[DefaultRunnerId].WaitForStop()
				h.shutdown()
			}()
		}
	}
	return h, nil
}

// ActiveRunners returns a copy of the runners slice
// This is used to understand the active runners for testing purposes
// It is not safe to use this method in production code
func (h *Handler) ActiveRunners() []*Runner {
	h.mu.Lock()
	defer h.mu.Unlock()
	runners := make([]*Runner, len(h.runners))
	copy(runners, h.runners)
	return runners
}

func (h *Handler) ExitCode() int {
	if h.cfg.UseProcedureMode {
		// No point aggregating across runners
		return 0
	} else {
		if h.runners[DefaultRunnerId] == nil {
			return 0
		}
		return h.runners[DefaultRunnerId].ExitCode()
	}

}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	hc, err := h.healthCheck()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		json.NewEncoder(w).Encode(hc)
	}
}

func (h *Handler) healthCheck() (*HealthCheck, error) {
	// FIXME: remove ready/busy IPC
	// Use Go runner as source of truth for readiness and concurrency
	log := logger.Sugar()
	var hc HealthCheck
	if h.cfg.UseProcedureMode {
		if err := writeReadyFile(); err != nil {
			log.Errorw("failed to write ready file", "error", err)
			return nil, err
		}
		hc = HealthCheck{
			Setup: &SetupResult{
				StartedAt:   util.FormatTime(h.startedAt),
				CompletedAt: util.FormatTime(h.startedAt),
				Status:      SetupSucceeded,
			},
			Concurrency: Concurrency{
				// Max runners as max concurrency
				Max: len(h.runners),
			},
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, runner := range h.runners {
			if runner == nil {
				continue
			}
			if runner.status == StatusDefunct || runner.status == StatusSetupFailed {
				h.runners[i] = nil
				log.Warnw("stopping stale runner", "name", runner.name, "status", runner.status.String())
				go func() {
					if err := runner.Stop(); err != nil {
						log.Errorw("failed to stop runner", "name", runner.name, "error", err)
					}
				}()
				continue
			}
			// Aggregate current concurrency across workers
			hc.Concurrency.Current += runner.Concurrency().Current
		}
		if hc.Concurrency.Current < hc.Concurrency.Max {
			hc.Status = StatusReady.String()
		} else {
			hc.Status = StatusBusy.String()
		}
	} else {
		hc = HealthCheck{
			Status:      h.runners[DefaultRunnerId].status.String(),
			Setup:       &h.runners[DefaultRunnerId].setupResult,
			Concurrency: h.runners[DefaultRunnerId].Concurrency(),
		}
	}
	return &hc, nil
}

func (h *Handler) OpenApi(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UseProcedureMode {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(procedureSchema))
		return
	}

	if h.runners[DefaultRunnerId].schema == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(h.runners[DefaultRunnerId].schema))
	}
}

func (h *Handler) Shutdown(w http.ResponseWriter, r *http.Request) {
	err := h.Stop()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) Stop() error {
	log := logger.Sugar()
	// Procedure mode and no runner yet
	if len(h.runners) == 0 {
		// Shut down immediately
		h.shutdown()
		return nil
	}

	// Stop all runners
	var err error = nil
	eg := errgroup.Group{}
	for _, runner := range h.runners {
		if runner == nil {
			continue
		}
		if err = runner.Stop(); err != nil {
			log.Errorw("failed to stop runner", "name", runner.name, "error", err)
		}
		eg.Go(func() error {
			runner.WaitForStop()
			return nil
		})
	}
	// Wait and shutdown
	go func() {
		if err := eg.Wait(); err != nil {
			log.Errorw("failed to wait for runners to stop", "error", err)
			os.Exit(1)
		}
		h.shutdown()
	}()
	return err
}

func (h *Handler) findRunnerWithName(name string) *Runner {
	for _, runner := range h.runners {
		if runner == nil {
			continue
		}
		if runner.name == name {
			return runner
		}
	}
	return nil
}

func (h *Handler) HandleIPC(w http.ResponseWriter, r *http.Request) {
	log := logger.Sugar()
	var ipc IPC
	bs, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(bs, &ipc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := DefaultRunnerName
	if h.cfg.UseProcedureMode {
		name = ipc.Name
	}
	if runner := h.findRunnerWithName(name); runner != nil {
		if err := runner.HandleIPC(ipc.Status); err != nil {
			log.Errorw("failed to handle IPC", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if !(h.cfg.UseProcedureMode && ipc.Status == IPCStatusReady) {
		// This happens for the first ready IPC after procedure setup succeeded and before the runner is registered
		// Safe to ignore in that case
		log.Warnw("runner not found for IPC", "pid", ipc.Pid, "name", ipc.Name)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) predictWithRunner(srcURL string, req PredictionRequest) (chan PredictionResponse, error) {
	log := logger.Sugar()

	// Lock before checking to avoid thrashing runner replacements
	h.mu.Lock()
	defer h.mu.Unlock()

	// Look for an existing runner copy for source URL in READY state
	// There might be multiple copies if the # pending predictions > max concurrency of a single runner
	// For non-async predictors, the same runner might occupy all runner slots, each serving 0 or 1 prediction
	runnerIdx := -1
	for i, runner := range h.runners {
		if runner == nil {
			if runnerIdx < 0 {
				// Memorize the first vacant index we see in case a new runner is needed
				runnerIdx = i
			}
			continue
		}
		// Match runner with spare capacity
		if strings.HasSuffix(runner.name, ":"+srcURL) && runner.Concurrency().Current < runner.Concurrency().Max {
			return runner.Predict(req)
		}
	}

	// No vacancy, need to evict one
	// FIXME: make this LRU or something more efficient
	if runnerIdx < 0 {
		for i, runner := range h.runners {
			if runner == nil {
				// Should not happen if no vacancy but anyway
				continue
			}
			if !runner.Idle() {
				continue
			}
			log.Infow("stopping procedure runner", "name", runner.name)
			if err := runner.Stop(); err != nil {
				log.Errorw("failed to stop runner", "error", err)
			}
			h.runners[i] = nil
			runnerIdx = i
			break
		}
	}

	// Failed to evict one, this should not happen, because:
	// We only enter this method when aggregated current concurrency < max concurrency, i.e. max runners
	// * In the case where some runners are async and serving > 1 predictions, there must be idle runners serving 0
	// * In the case where all runners are non-async, at least one must be idle
	if runnerIdx == -1 {
		log.Errorw("failed to find idle runner to evict", "src_url", srcURL)
		return nil, ErrConflict
	}

	// Name is index + source URL in case multiple instances of the same URL
	name := fmt.Sprintf("%02d:%s", runnerIdx, srcURL)

	// Start new runner
	srcDir, err := util.PrepareProcedureSourceURL(srcURL, runnerIdx)
	if err != nil {
		return nil, err
	}
	uid, err := h.uidCounter.allocate()
	if err != nil {
		return nil, err
	}
	if h.setUID {
		// PrepareProcedureSourceURL copies all directories and files
		// Change ownership to unprivileged user
		err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Lchown(path, uid, NoGroupGID)
		})
		if err != nil {
			return nil, err
		}
	}

	log.Infow("starting procedure runner", "src_url", srcURL, "src_dir", srcDir)
	r, err := NewProcedureRunner(name, srcDir, h.cfg)
	if err != nil {
		return nil, err
	}

	if h.setUID {
		// Make working dir writable by unprivileged Python process
		if err := os.Lchown(r.workingDir, uid, NoGroupGID); err != nil {
			return nil, err
		}
		// Create per runner TMPDIR
		tmpDir, err := os.MkdirTemp("", "cog-runner-tmp-")
		if err != nil {
			return nil, err
		}
		if err := os.Lchown(tmpDir, uid, NoGroupGID); err != nil {
			return nil, err
		}
		// Set temp directory for cleanup and environment
		r.tmpDir = tmpDir
		r.cmd.Env = os.Environ()
		r.cmd.Env = append(r.cmd.Env, "TMPDIR="+tmpDir)

		// Use syscall.Credential to run process as unprivileged user from start
		// This eliminates the need for Python process to call setuid()
		r.cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(NoGroupGID),
			},
		}
	}

	if err := r.Start(); err != nil {
		return nil, err
	}
	start := time.Now()
	// Wait for runner to become ready, this should not take long as procedures have no setup
	for {
		// We do not register non-ready runner yet for HTTP IPC due to potential race condition
		// Instead we poll here for a ready file and status change
		readyFile := path.Join(r.workingDir, "ready")
		if _, err := os.Stat(readyFile); err == nil {
			if err := r.HandleIPC(IPCStatusReady); err != nil {
				log.Errorw("failed to handle IPC", "error", err)
				return nil, fmt.Errorf("failed to handle IPC: %w", err)
			}
			if err := os.Remove(readyFile); err != nil && !os.IsNotExist(err) {
				log.Errorw("failed to remove ready file", "error", err)
			}
		}
		// No ready file if runner dies but status should be setup failed
		if r.status != StatusStarting {
			break
		}
		if time.Since(start) > 10*time.Second {
			log.Errorw("stopping procedure runner after time out", "elapsed", time.Since(start))
			if err := r.Stop(); err != nil {
				log.Errorw("failed to stop procedure runner", "error", err)
			}
			return nil, fmt.Errorf("procedure time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.status == StatusSetupFailed {
		log.Errorw("procedure runner setup failed", "logs", r.setupResult.Logs)

		// Translate setup failure to prediction failure
		resp := PredictionResponse{
			Input:       req.Input,
			Id:          req.Id,
			CreatedAt:   r.setupResult.StartedAt,
			StartedAt:   r.setupResult.StartedAt,
			CompletedAt: r.setupResult.CompletedAt,
			Logs:        r.setupResult.Logs,
			Status:      PredictionFailed,
			Error:       ErrSetupFailed.Error(),
		}
		if req.Webhook == "" {
			c := make(chan PredictionResponse, 1)
			c <- resp
			return c, nil
		} else {
			// Async prediction, send webhook
			go func() {
				if err := SendWebhook(req.Webhook, &resp); err != nil {
					log.Errorw("failed to send webhook", "url", "error", err)
				}
			}()
			return nil, nil
		}

	}
	h.runners[runnerIdx] = r
	return r.Predict(req)
}

func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
		return
	}
	var req PredictionRequest
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
		if req.Id != "" && req.Id != id {
			http.Error(w, "prediction ID mismatch", http.StatusBadRequest)
			return
		}
		req.Id = id
	}
	if req.Id == "" {
		req.Id, err = util.PredictionId()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	var c chan PredictionResponse
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
		procedureSourceUrl := val.(string)

		val, ok = req.Context["replicate_api_token"]
		if !ok {
			http.Error(w, "missing replicate_api_token in context", http.StatusBadRequest)
			return
		}

		token := val.(string)
		if procedureSourceUrl == "" || token == "" {
			http.Error(w, "empty procedure_source_url or replicate_api_token", http.StatusBadRequest)
			return
		}
		c, err = h.predictWithRunner(procedureSourceUrl, req)
	} else {
		c, err = h.runners[DefaultRunnerId].Predict(req)
	}

	if errors.Is(err, ErrConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if errors.Is(err, ErrDefunct) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	} else if errors.Is(err, ErrExists) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if errors.Is(err, ErrSetupFailed) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if c == nil {
		w.WriteHeader(http.StatusAccepted)
		resp := PredictionResponse{Id: req.Id, Status: "starting"}
		writeResponse(w, resp)
	} else {
		resp := <-c
		w.WriteHeader(http.StatusOK)
		writeResponse(w, resp)
	}
}

func writeBytes(w http.ResponseWriter, bs []byte) {
	log := logger.Sugar()
	if _, err := w.Write(bs); err != nil {
		log.Errorw("failed to write response", "error", err)
	}
}

func writeResponse(w http.ResponseWriter, resp PredictionResponse) {
	log := logger.Sugar()
	bs, err := json.Marshal(resp)
	if err != nil {
		log.Errorw("failed to marshal response", "error", err)
	}
	writeBytes(w, bs)
}

func SendWebhook(webhook string, pr *PredictionResponse) error {
	body, err := json.Marshal(pr)
	if err != nil {
		return fmt.Errorf("failed to marshal prediction response: %w", err)
	}
	req, err := http.NewRequest("POST", webhook, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	// Only retry on completed webhooks
	client := http.DefaultClient
	if pr.Status.IsCompleted() {
		client = util.HTTPClientWithRetry()
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
	// Procedure mode and no runner yet
	if len(h.runners) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	id := r.PathValue("id")
	// We don't know which runner has the prediction, so try all of them
	for _, runner := range h.runners {
		if err := runner.Cancel(id); err == nil {
			w.WriteHeader(http.StatusOK)
			return
		} else if errors.Is(err, ErrNotFound) {
			continue
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
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
