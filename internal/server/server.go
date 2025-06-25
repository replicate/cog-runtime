package server

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"github.com/replicate/go/must"

	"golang.org/x/sync/errgroup"

	"io"
	"net/http"
	"sync"
	"time"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/go/logging"
)

var logger = logging.New("cog-http-server")

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
	cfg        Config
	shutdown   context.CancelFunc
	startedAt  time.Time
	maxRunners int
	runners    map[string]*Runner
	mu         sync.Mutex
}

func NewHandler(cfg Config, shutdown context.CancelFunc) (*Handler, error) {
	log := logger.Sugar()
	h := &Handler{
		cfg:       cfg,
		shutdown:  shutdown,
		startedAt: time.Now(),
		runners:   make(map[string]*Runner),
	}
	// GOMAXPROCS is set by automaxprocs in main.go on server startup
	// Reset Go server to 1 to make room for Python runners
	autoMaxProcs := runtime.GOMAXPROCS(1)
	if cfg.UseProcedureMode {
		// At least 2 Python runners in procedure mode so that:
		// * Server status is READY if available runner slot >= 1, either empty or IDLE
		// * The IDLE runner can be evicted for one with a new procedure source URL
		h.maxRunners = max(autoMaxProcs, 2)
		log.Infow("running in procedure mode", "max_runners", h.maxRunners)
	} else {
		h.runners[DefaultRunner] = NewRunner(cfg.IPCUrl, cfg.UploadUrl)
		if err := h.runners[DefaultRunner].Start(); err != nil {
			return nil, err
		}

		if !cfg.AwaitExplicitShutdown {
			go func() {
				// Shut down as soon as runner exists
				h.runners[DefaultRunner].WaitForStop()
				h.shutdown()
			}()
		}
	}
	return h, nil
}

func (h *Handler) ExitCode() int {
	if h.cfg.UseProcedureMode {
		// No point aggregating across runners
		return 0
	} else {
		if h.runners[DefaultRunner] == nil {
			return 0
		}
		return h.runners[DefaultRunner].ExitCode()
	}

}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	log := logger.Sugar()
	var hc HealthCheck
	if h.cfg.UseProcedureMode {
		hc = HealthCheck{
			Setup: &SetupResult{
				StartedAt:   util.FormatTime(h.startedAt),
				CompletedAt: util.FormatTime(h.startedAt),
				Status:      SetupSucceeded,
			},
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		hasIdle := false
		toRemove := make([]string, 0)
		for name, runner := range h.runners {
			if runner.status == StatusDefunct || runner.status == StatusSetupFailed {
				toRemove = append(toRemove, name)
				log.Warnw("stopping stale runner", "name", name, "status", runner.status.String())
				go func() {
					if err := runner.Stop(); err != nil {
						log.Errorw("failed to stop runner", "name", name, "error", err)
					}
				}()
				continue
			}
			if runner.Idle() {
				hasIdle = true
			}
		}
		// In procedure mode, a server is only READY if available runner slot >= 1, either empty or IDLE.
		// In the case of a request with a new procedure source URL, the IDLE runner can be evicted.
		// Otherwise, we report BUSY even if all runners are READY but not IDLE, e.g. len(pending) > 0.
		for _, name := range toRemove {
			delete(h.runners, name)
		}
		if len(h.runners) < h.maxRunners || hasIdle {
			hc.Status = StatusReady.String()
		} else {
			hc.Status = StatusBusy.String()
		}
	} else {
		hc = HealthCheck{
			Status: h.runners[DefaultRunner].status.String(),
			Setup:  &h.runners[DefaultRunner].setupResult,
		}
	}

	if bs, err := json.Marshal(hc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, bs)
	}
}

func (h *Handler) OpenApi(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UseProcedureMode {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(procedureSchema))
		return
	}

	if h.runners[DefaultRunner].schema == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, []byte(h.runners[DefaultRunner].schema))
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
	for name, runner := range h.runners {
		if err = runner.Stop(); err != nil {
			log.Errorw("failed to stop runner", "name", name, "err", err)
		}
		eg.Go(func() error {
			runner.WaitForStop()
			return nil
		})
	}
	// Wait and shutdown
	go func() {
		must.Do(eg.Wait())
		h.shutdown()
	}()
	return err
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
	name := DefaultRunner
	if h.cfg.UseProcedureMode {
		name = ipc.Name
	}
	if runner, ok := h.runners[name]; ok {
		runner.HandleIPC(ipc.Status)
	} else {
		fmt.Println(h.runners)
		log.Warnw("runner not found for IPC", "pid", ipc.Pid, "name", ipc.Name)
	}
}

func (h *Handler) getRunner(srcURL, srcDir string) (*Runner, error) {
	log := logger.Sugar()

	// Lock before checking to avoid thrashing runner replacements
	h.mu.Lock()
	defer h.mu.Unlock()

	// Reuse current runner, nothing to do
	if runner, ok := h.runners[srcURL]; ok {
		return runner, nil
	}

	// Need to evict one
	if len(h.runners) == h.maxRunners {
		for name, runner := range h.runners {
			if !runner.Idle() {
				continue
			}
			log.Infow("stopping procedure runner", "src_url", name)
			if err := runner.Stop(); err != nil {
				log.Errorw("failed to stop runner", "error", err)
			} else {
				delete(h.runners, name)
				break
			}
		}
	}
	if len(h.runners) == h.maxRunners {
		return nil, ErrConflict
	}

	// Start new runner
	log.Infow("starting procedure runner", "src_url", srcURL)
	r := NewProcedureRunner(h.cfg.IPCUrl, h.cfg.UploadUrl, srcURL, srcDir)
	h.runners[srcURL] = r

	if err := r.Start(); err != nil {
		return nil, err
	}
	start := time.Now()
	// Wait for runner to become ready, this should not take long as procedures have no setup
	for {
		if r.status == StatusReady {
			break
		}
		if r.status == StatusSetupFailed {
			log.Errorw("procedure runner setup failed", "logs", r.setupResult.Logs)
			delete(h.runners, srcURL)
			// Include failed runner here so that the caller can extract setup logs and respond with a prediction failure
			return r, ErrSetupFailed
		}
		if time.Since(start) > 10*time.Second {
			delete(h.runners, srcURL)
			log.Errorw("stopping procedure runner after time out", "elapsed", time.Since(start))
			if err := r.Stop(); err != nil {
				log.Errorw("failed to stop procedure runner", "error", err)
			}
			return nil, fmt.Errorf("procedure time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r, nil
}

func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	log := logger.Sugar()
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
		req.Id = util.PredictionId()
	}

	var runner *Runner
	if h.cfg.UseProcedureMode {
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
		srcDir, err := util.PrepareProcedureSourceURL(procedureSourceUrl)
		if err != nil {
			http.Error(w, "invalid procedure_source_url", http.StatusBadRequest)
		}
		if r, err := h.getRunner(procedureSourceUrl, srcDir); err == nil {
			runner = r
		} else if errors.Is(err, ErrConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		} else if errors.Is(err, ErrSetupFailed) {
			// Translate setup failure to prediction failure
			resp := PredictionResponse{
				Input:       req.Input,
				Id:          req.Id,
				CreatedAt:   r.setupResult.StartedAt,
				StartedAt:   r.setupResult.StartedAt,
				CompletedAt: r.setupResult.CompletedAt,
				Logs:        r.setupResult.Logs,
				Status:      PredictionFailed,
			}

			if req.Webhook == "" {
				w.WriteHeader(http.StatusOK)
				writeResponse(w, resp)
			} else {
				w.WriteHeader(http.StatusAccepted)
				writeResponse(w, PredictionResponse{Id: req.Id, Status: "starting"})
				if err := SendWebhook(req.Webhook, &resp); err != nil {
					log.Errorw("failed to send webhook", "url", "error", err)
				}
			}
			return
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		runner = h.runners[DefaultRunner]
	}

	c, err := runner.Predict(req)
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
	body := bytes.NewBuffer(must.Get(json.Marshal(pr)))
	req := must.Get(http.NewRequest("POST", webhook, body))
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
