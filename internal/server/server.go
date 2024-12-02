package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/go/must"

	"github.com/replicate/go/logging"
)

var logger = logging.New("cog-http-server")

type Handler struct {
	runner *Runner
}

func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	hc := HealthCheck{
		Status: h.runner.status.String(),
		Setup:  h.runner.setupResult,
	}

	if bs, err := json.Marshal(hc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write(bs))
	}
}

func (h *Handler) OpenApi(w http.ResponseWriter, r *http.Request) {
	if h.runner.schema == "" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
		must.Get(w.Write([]byte(h.runner.schema)))
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

	c, err := h.runner.predict(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
