package server

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"

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
		concurrencyPerCPU := 4
		if s, ok := os.LookupEnv("COG_PROCEDURE_CONCURRENCY_PER_CPU"); ok {
			if i, err := strconv.Atoi(s); err == nil {
				concurrencyPerCPU = i
			} else {
				log.Errorw("failed to parse COG_PROCEDURE_CONCURRENCY_PER_CPU", "value", s)
			}
		}
		// Set both max runners and max concurrency across all runners to CPU * n,
		// regardless what max concurrency each runner has.
		// In the worst case scenario where all runners are non-async,
		// completion of any runner frees up concurrency.
		h.maxRunners = autoMaxProcs * concurrencyPerCPU
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
	if bs, err := json.Marshal(h.healthCheck()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		writeBytes(w, bs)
	}
}

func (h *Handler) healthCheck() *HealthCheck {
	// FIXME: remove ready/busy IPC
	// Use Go runner as source of truth for readiness and concurrency
	log := logger.Sugar()
	var hc HealthCheck
	if h.cfg.UseProcedureMode {
		hc = HealthCheck{
			Setup: &SetupResult{
				StartedAt:   util.FormatTime(h.startedAt),
				CompletedAt: util.FormatTime(h.startedAt),
				Status:      SetupSucceeded,
			},
			Concurrency: Concurrency{
				// Max runners as max concurrency
				Max: h.maxRunners,
			},
		}
		h.mu.Lock()
		defer h.mu.Unlock()
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
			// Aggregate current concurrency across workers
			hc.Concurrency.Current += runner.Concurrency().Current
		}
		for _, name := range toRemove {
			delete(h.runners, name)
		}
		if hc.Concurrency.Current < hc.Concurrency.Max {
			hc.Status = StatusReady.String()
		} else {
			hc.Status = StatusBusy.String()
		}
	} else {
		hc = HealthCheck{
			Status:      h.runners[DefaultRunner].status.String(),
			Setup:       &h.runners[DefaultRunner].setupResult,
			Concurrency: h.runners[DefaultRunner].Concurrency(),
		}
	}
	return &hc
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
			log.Errorw("failed to stop runner", "name", name, "error", err)
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

func (h *Handler) predictWithRunner(srcURL string, req PredictionRequest) (chan PredictionResponse, error) {
	log := logger.Sugar()

	// Lock before checking to avoid thrashing runner replacements
	h.mu.Lock()
	defer h.mu.Unlock()

	// Look for an existing runner copy for source URL in READY state
	// There might be multiple copies if the # pending predictions > max concurrency of a single runner
	// For non-async predictors, the same runner might occupy all runner slots
	for i := 0; i <= h.maxRunners; i++ {
		name := fmt.Sprintf("%02d:%s", i, srcURL)
		runner, ok := h.runners[name]
		if ok && runner.Concurrency().Current < runner.Concurrency().Max {
			return runner.Predict(req)
		}
	}

	// Need to evict one
	if len(h.runners) == h.maxRunners {
		for name, runner := range h.runners {
			if !runner.Idle() {
				continue
			}
			log.Infow("stopping procedure runner", "name", name)
			if err := runner.Stop(); err != nil {
				log.Errorw("failed to stop runner", "error", err)
			} else {
				delete(h.runners, name)
				break
			}
		}
	}
	// Failed to evict one, this should not happen
	if len(h.runners) == h.maxRunners {
		log.Errorw("failed to find idle runner to evict", "src_url", srcURL)
		return nil, ErrConflict
	}

	// Find the first available slot for the new runner copy
	var name string
	var slot int
	for i := 0; i <= h.maxRunners; i++ {
		n := fmt.Sprintf("%02d:%s", i, srcURL)
		if _, ok := h.runners[n]; !ok {
			name = n
			slot = i
			break
		}
	}
	// Max out slots, this should not happen
	if name == "" {
		log.Errorw("reached max copies of runner", "src_url", srcURL)
		return nil, ErrConflict
	}

	// Start new runner
	srcDir, err := util.PrepareProcedureSourceURL(srcURL, slot)
	if err != nil {
		return nil, err
	}
	log.Infow("starting procedure runner", "src_url", srcURL, "src_dir", srcDir)
	r := NewProcedureRunner(h.cfg.IPCUrl, h.cfg.UploadUrl, name, srcDir)
	h.runners[name] = r

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
			delete(h.runners, name)

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
		if time.Since(start) > 10*time.Second {
			delete(h.runners, name)
			log.Errorw("stopping procedure runner after time out", "elapsed", time.Since(start))
			if err := r.Stop(); err != nil {
				log.Errorw("failed to stop procedure runner", "error", err)
			}
			return nil, fmt.Errorf("procedure time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
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
		req.Id = util.PredictionId()
	}

	var c chan PredictionResponse
	if h.cfg.UseProcedureMode {
		// Although individual runners may have higher concurrency than the global max runners/concurrency
		// We still bail early if the global max has been reached
		concurrency := h.healthCheck().Concurrency
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
		c, err = h.runners[DefaultRunner].Predict(req)
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
