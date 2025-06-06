package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"syscall"
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
	startedAt time.Time
	runner    *Runner
	mu        sync.Mutex
}

func NewHandler(cfg Config) *Handler {
	h := &Handler{
		cfg:       cfg,
		startedAt: time.Now(),
	}
	if !cfg.UseProcedureMode {
		h.runner = NewRunner(cfg.AwaitExplicitShutdown, cfg.UploadUrl)
		must.Do(h.runner.Start())
	}
	return h
}

func (h *Handler) Stop() error {
	if h.runner == nil {
		return nil
	}
	return h.runner.Stop(true)
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
	// Procedure mode and no runner yet
	if h.runner == nil {
		// SIGTERM self to shut down HTTP server
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.runner.Stop(true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) updateRunner(srcDir, token string) error {
	log := logger.Sugar()

	// Reuse current runner, nothing to do
	if h.runner != nil && h.runner.SrcDir() == srcDir {
		return nil
	}

	// Need to start a new runner, lock until done
	h.mu.Lock()
	defer h.mu.Unlock()

	// Different source URL, stop current runner
	if h.runner != nil {
		log.Infow("stopping procedure runner", "src_dir", h.runner.SrcDir())
		if err := h.runner.Stop(false); err != nil {
			log.Errorw("failed to stop runner", "error", err)
		}
		h.runner = nil
	}

	// Start new runner
	log.Infow("starting procedure runner", "src_dir", srcDir)
	runner := NewProcedureRunner(h.cfg.AwaitExplicitShutdown, h.cfg.UploadUrl, srcDir)
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
			if err := runner.Stop(false); err != nil {
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
		if req.ProcedureSourceURL == "" || req.Token == "" {
			http.Error(w, "missing procedure_source_url or token", http.StatusBadRequest)
			return
		}
		u, err := url.Parse(req.ProcedureSourceURL)
		if err != nil {
			http.Error(w, "invalid procedure_source_url", http.StatusBadRequest)
			return
		}
		srcDir := u.Path
		if err := h.updateRunner(srcDir, req.Token); err != nil {
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
