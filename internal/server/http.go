package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/replicate/go/logging"
)

var (
	ErrConflict    = errors.New("already running a prediction")
	ErrExists      = errors.New("prediction exists")
	ErrNotFound    = errors.New("prediction not found")
	ErrDefunct     = errors.New("server is defunct")
	ErrSetupFailed = errors.New("setup failed")
)

var log = logging.New("server")

func NewServer(addr string, handler *Handler, useProcedureMode bool) *http.Server {
	log := log.Sugar()
	serveMux := http.NewServeMux()
	serveMux.HandleFunc("GET /{$}", handler.Root)
	serveMux.HandleFunc("GET /health-check", handler.HealthCheck)
	serveMux.HandleFunc("GET /openapi.json", handler.OpenApi)
	serveMux.HandleFunc("POST /shutdown", handler.Shutdown)

	if useProcedureMode {
		serveMux.HandleFunc("POST /procedures", handler.Predict)
		serveMux.HandleFunc("PUT /procedures/{id}", handler.Predict)
		serveMux.HandleFunc("POST /procedures/{id}/cancel", handler.Cancel)
	} else {
		serveMux.HandleFunc("POST /predictions", handler.Predict)
		serveMux.HandleFunc("PUT /predictions/{id}", handler.Predict)
		serveMux.HandleFunc("POST /predictions/{id}/cancel", handler.Cancel)
	}

	serveMux.HandleFunc("POST /_ipc", handler.HandleIPC)

	if _, ok := os.LookupEnv("TEST_COG"); ok {
		// We run Go server with go run ... which spawns a new process
		// Report its PID via HTTP instead
		serveMux.HandleFunc("/_pid", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(strconv.Itoa(os.Getpid())))
			if err != nil {
				log.Errorw("failed to write pid", "error", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		})
		// Also report runners for procedure tests
		serveMux.HandleFunc("/_runners", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			var runners []string
			for _, runner := range handler.runners {
				if runner == nil {
					continue
				}
				runners = append(runners, runner.name)
			}
			json, err := json.Marshal(runners)
			if err != nil {
				log.Errorw("failed to marshal runners", "error", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, err = w.Write(json)
			if err != nil {
				log.Errorw("failed to write runners", "error", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		})
	}

	return &http.Server{
		Addr:    addr,
		Handler: serveMux,
	}
}
