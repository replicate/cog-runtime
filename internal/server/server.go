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
		if cfg.OneShot {
			// In one-shot mode, force single runner slot
			maxRunners = 1
		} else if maxRunners == 0 {
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
		h.runners[DefaultRunnerID] = runner
		// Since we do not have a server context, this ia tempoarary TODO context for the runner
		ctx := context.TODO() //nolint:contextcheck // context passing not viable in current architecture, this is a temporary context until we have a server root context
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		if err := h.runners[DefaultRunnerID].Start(ctx); err != nil {
			return nil, err
		}
		eg := errgroup.Group{}
		deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 10*time.Second)
		defer deadlineCancel()
		eg.Go(func() error {
			err := h.runners[DefaultRunnerID].config(deadlineCtx)
			if err != nil {
				cancel(fmt.Errorf("failed to config runner: %w", err))
				// Stop the runner to avoid it hanging around, r.wait() cannot be canceled via context yet
				// NOTE(morgan): configuration failure means we do not want to give a graceperiod to the runner
				h.runners[DefaultRunnerID].shutdownGracePeriod = 0
				if err := h.runners[DefaultRunnerID].Stop(); err != nil {
					log.Errorw("failed to stop runner", "error", err)
				}
			}
			return err
		})

		// TODO: this should be tied to the runner's context, derived from the server context
		// r.wait cannot use the egCtx because eg.Wait() will cancel it, so instead we need to leverage the r.config() to
		// cancel the runner's wait context. r.wait also cannot be in the errorgroup, as it must continue to run until
		// r.Stop() or r.ForceKill() is called.
		go h.runners[DefaultRunnerID].wait()

		if err := eg.Wait(); err != nil {
			return nil, err
		}

		if !cfg.AwaitExplicitShutdown {
			go func() {
				// Shut down as soon as runner exists
				h.runners[DefaultRunnerID].WaitForStop()
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
	}
	if h.runners[DefaultRunnerID] == nil {
		return 0
	}
	return h.runners[DefaultRunnerID].ExitCode()
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
	// FIXME: remove ready/busy IPC
	// Use Go runner as source of truth for readiness and concurrency
	log := logger.Sugar()
	var hc HealthCheck
	if !h.cfg.UseProcedureMode {
		return &HealthCheck{
			Status:      h.runners[DefaultRunnerID].status.String(),
			Setup:       &h.runners[DefaultRunnerID].setupResult,
			Concurrency: h.runners[DefaultRunnerID].Concurrency(),
		}, nil
	}

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

	// Determine status
	hc.Status = StatusBusy.String()
	if hc.Concurrency.Current < hc.Concurrency.Max && !h.cleanupInProgress() {
		hc.Status = StatusReady.String()
	}

	return &hc, nil
}

func (h *Handler) cleanupInProgress() bool {
	if !h.cfg.OneShot {
		return false
	}

	if len(h.runners) == 0 || h.runners[0] == nil {
		return false
	}

	return len(h.runners[0].cleanupSlot) == 0
}

func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UseProcedureMode {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(procedureSchema))
		return
	}

	if h.runners[DefaultRunnerID].schema == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(h.runners[DefaultRunnerID].schema))
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

// ForceKillAll immediately force-kills all runners (for test cleanup)
func (h *Handler) ForceKillAll() {
	for _, runner := range h.runners {
		if runner != nil {
			runner.ForceKill()
		}
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
	var err error
	eg := errgroup.Group{}
	for _, runner := range h.runners {
		if runner == nil { //nolint:revive // nil check is intentional, we want to skip the nil slot
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
	} else if !h.cfg.UseProcedureMode || ipc.Status != IPCStatusReady {
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
				Uid: uint32(uid), //nolint:gosec // this is guarded in isolation .allocate, cannot exceed const MaxUID
				Gid: uint32(NoGroupGID),
			},
		}
	}

	setupComplete := make(chan struct{})
	ctx := context.TODO() //nolint:contextcheck // context passing not viable in current architecture, this is a temporary context until we have a server root context
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 10*time.Second)
	defer deadlineCancel()
	if err := r.Start(ctx); err != nil {
		return nil, err
	}
	start := time.Now()

	// TODO: this should be tied to the runner's context, derived from the server context
	// r.wait cannot use the egCtx because eg.Wait() will cancel it, so instead we need to leverage the r.config() to
	// cancel the runner's wait context. r.wait also cannot be in the errorgroup, as it must continue to run until
	// r.Stop() or r.ForceKill() is called.
	go r.wait()

	eg := errgroup.Group{}
	eg.Go(func() error {
		err := r.config(deadlineCtx)
		if err != nil {
			cancel(fmt.Errorf("failed to config runner: %w", err))
			return err
		}
		return nil
	})
	eg.Go(func() error {
		// This goroutine is tasked with handling a setup timeout and stopping the runner
		// if "setupComplete" is closed, we know we didn't hit the timeout case.
		//
		// In theory this could be moved in the config .Go() run but for clarity we isolate since
		// errors in .config() or below in the .Go() run below are both cases to .Stop() the runner.
		select {
		case <-setupComplete:
			return nil
		case <-deadlineCtx.Done():
			log.Errorw("stopping procedure runner after timeout", "elapsed", time.Since(start))
			// Stop the runner to avoid it hanging around, r.wait() cannot be canceled via context yet
			// NOTE(morgan): failure here means we do not want to give a graceperiod to the runner
			r.shutdownGracePeriod = 0
			err := r.Stop()
			if err != nil {
				log.Errorw("failed to stop runner", "error", err)
			}
			if errors.Is(deadlineCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w: timeout waiting for runner to config", context.DeadlineExceeded)
			}
			return deadlineCtx.Err()
		}
	})
	eg.Go(func() error {
		// Wait for runner to become ready, this should not take long as procedures have no setup
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			// We do not register non-ready runner yet for HTTP IPC due to potential race condition
			// Instead we poll here for a ready file and status change
			readyFile := path.Join(r.workingDir, "ready")
			if _, err := os.Stat(readyFile); err == nil {
				if err := r.HandleIPC(IPCStatusReady); err != nil {
					log.Errorw("failed to handle IPC", "error", err)
					return fmt.Errorf("failed to handle IPC: %w", err)
				}
				if err := os.Remove(readyFile); err != nil && !os.IsNotExist(err) {
					log.Errorw("failed to remove ready file", "error", err)
				}
			}
			// No ready file if runner dies but status should be setup failed
			if r.status != StatusStarting {
				// This is the ONLY goroutine tasked with closing the setupComplete channel
				close(setupComplete)
				return nil
			}
			select {
			case <-ticker.C:
				continue
			case <-deadlineCtx.Done():
				if errors.Is(deadlineCtx.Err(), context.DeadlineExceeded) {
					return fmt.Errorf("%w: timeout waiting for runner to config", context.DeadlineExceeded)
				}
				return deadlineCtx.Err()
			}
		}
	})
	// wait for the errorgroup to complete, we expect "setup completed" "setup failed" or "timeout"
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if r.status == StatusSetupFailed {
		log.Errorw("procedure runner setup failed", "logs", r.setupResult.Logs)

		// Translate setup failure to prediction failure
		resp := PredictionResponse{
			Input:       req.Input,
			ID:          req.ID,
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
		}
		// Async prediction, send webhook
		go func() {
			if err := SendWebhook(req.Webhook, &resp); err != nil {
				log.Errorw("failed to send webhook", "url", req.Webhook, "error", err)
			}
		}()
		return nil, errAsyncPrediction

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
		if req.ID != "" && req.ID != id {
			http.Error(w, "prediction ID mismatch", http.StatusBadRequest)
			return
		}
		req.ID = id
	}
	if req.ID == "" {
		req.ID, err = util.PredictionID()
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
		c, err = h.predictWithRunner(procedureSourceURL, req) //nolint:contextcheck // context passing not viable in current architecture
	} else {
		c, err = h.runners[DefaultRunnerID].Predict(req)
	}

	switch {
	case errors.Is(err, errAsyncPrediction):
		// Async prediction sentinel received this explicitly means
		// we fall through and hit the `c == nil` if branch below
		break
	case errors.Is(err, ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
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
	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewBuffer(body))
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
		var err error
		if err = runner.Cancel(id); err == nil {
			w.WriteHeader(http.StatusOK)
			return
		} else if errors.Is(err, ErrNotFound) {
			continue
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
