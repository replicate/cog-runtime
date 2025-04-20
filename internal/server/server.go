package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/go/must"

	"github.com/replicate/go/logging"
)

var logger = logging.New("cog-http-server")

type Handler struct {
	runner Runner
}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	sr := h.runner.SetupResult()
	hc := HealthCheck{
		Status: h.runner.Status().String(),
		Setup:  &sr,
	}

	if bs, err := json.Marshal(hc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write(bs))
	}
}

func (h *Handler) OpenApi(w http.ResponseWriter, r *http.Request) {
	if h.runner.Schema() == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write([]byte(h.runner.Schema())))
	}
}

func (h *Handler) Shutdown(w http.ResponseWriter, r *http.Request) {
	if err := h.runner.Shutdown(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	log := logger.Sugar()

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
		return
	}
	req := &PredictionRequest{}
	if err := json.Unmarshal(must.Get(io.ReadAll(r.Body)), req); err != nil {
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

	c, err := h.runner.Predict(req)
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
		log.Debug("response channel is nil; sending async response")
		w.WriteHeader(http.StatusAccepted)
		resp := PredictionResponse{Id: req.Id, Status: "starting"}
		must.Get(w.Write(must.Get(json.Marshal(resp))))
	} else {
		log.Debug("waiting for response on channel")
		resp := <-c
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write(must.Get(json.Marshal(resp))))
	}
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.runner.Cancel(id); err == nil {
		w.WriteHeader(http.StatusOK)
	} else if errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
