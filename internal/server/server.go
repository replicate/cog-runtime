package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/go/must"

	"github.com/replicate/go/logging"
)

var logger = logging.New("cog-http-server")

//go:embed openapi-procedure.json
var procedureSchema string

type Handler struct {
	cfg       Config
	shutdown  context.CancelFunc
	startedAt time.Time
	runner    *Runner
	mu        sync.Mutex
}

func NewHandler(cfg Config, shutdown context.CancelFunc) *Handler {
	h := &Handler{
		cfg:       cfg,
		shutdown:  shutdown,
		startedAt: time.Now(),
	}
	if !cfg.UseProcedureMode {
		h.runner = NewRunner(cfg.UploadUrl)
		must.Do(h.runner.Start())
		if !cfg.AwaitExplicitShutdown {
			go func() {
				// Shut down as soon as runner exists
				h.runner.WaitForStop()
				h.shutdown()
			}()
		}
	}
	return h
}

func (h *Handler) ExitCode() int {
	if h.runner == nil {
		return 0
	}
	return h.runner.ExitCode()
}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	var hc HealthCheck
	if h.cfg.UseProcedureMode {
		hc = HealthCheck{
			Status: StatusReady.String(),
			Setup: &SetupResult{
				StartedAt:   util.FormatTime(h.startedAt),
				CompletedAt: util.FormatTime(h.startedAt),
				Status:      SetupSucceeded,
			},
		}
		if h.runner != nil {
			hc.Status = h.runner.status.String()
		}
	} else {
		hc = HealthCheck{
			Status: h.runner.status.String(),
			Setup:  &h.runner.setupResult,
		}
	}

	if bs, err := json.Marshal(hc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write(bs))
	}
}

func (h *Handler) OpenApi(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UseProcedureMode {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write([]byte(procedureSchema)))
		return
	}

	if h.runner.schema == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write([]byte(h.runner.schema)))
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
	// Procedure mode and no runner yet
	if h.runner == nil {
		// Shut down immediately
		h.shutdown()
		return nil
	}

	// Request runner stop
	if err := h.runner.Stop(); err != nil {
		return err
	}
	go func() {
		// Shut down once runner exists
		h.runner.WaitForStop()
		h.shutdown()
	}()
	return nil
}

func (h *Handler) updateRunner(srcDir string) error {
	log := logger.Sugar()

	// Lock before checking to avoid thrashing runner replacements
	h.mu.Lock()
	defer h.mu.Unlock()

	// Reuse current runner, nothing to do
	if h.runner != nil && h.runner.SrcDir() == srcDir {
		return nil
	}

	// Different source URL, stop current runner
	if h.runner != nil {
		log.Infow("stopping procedure runner", "src_dir", h.runner.SrcDir())
		if err := h.runner.Stop(); err != nil {
			log.Errorw("failed to stop runner", "error", err)
		}
		h.runner = nil
	}

	// Start new runner
	log.Infow("starting procedure runner", "src_dir", srcDir)
	runner := NewProcedureRunner(h.cfg.UploadUrl, srcDir)
	if err := runner.Start(); err != nil {
		return err
	}
	start := time.Now()
	// Wait for runner to become ready, this should not take long as procedures have no setup
	for {
		if runner.status == StatusReady {
			break
		}
		if time.Since(start) > 10*time.Second {
			log.Errorw("stopping procedure runner after time out", "elapsed", time.Since(start))
			if err := runner.Stop(); err != nil {
				log.Errorw("failed to stop runner", "error", err)
			}
			return fmt.Errorf("procedure time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.runner = runner
	return nil
}

func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
		return
	}
	var req PredictionRequest
	if err := json.Unmarshal(must.Get(io.ReadAll(r.Body)), &req); err != nil {
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
		if err := h.updateRunner(srcDir); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	c, err := h.runner.predict(req)
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
		must.Get(w.Write(must.Get(json.Marshal(resp))))
	} else {
		resp := <-c
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write(must.Get(json.Marshal(resp))))
	}
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	// Procedure mode and no runner yet
	if h.runner == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	id := r.PathValue("id")
	if err := h.runner.cancel(id); err == nil {
		w.WriteHeader(http.StatusOK)
	} else if errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
