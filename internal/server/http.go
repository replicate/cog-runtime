package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/replicate/go/must"
)

var (
	ErrConflict    = errors.New("already running a prediction")
	ErrExists      = errors.New("prediction exists")
	ErrNotFound    = errors.New("prediction not found")
	ErrDefunct     = errors.New("server is defunct")
	ErrSetupFailed = errors.New("setup failed")
)

func NewServer(addr string, handler *Handler, useProcedureMode bool) *http.Server {
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
			must.Get(w.Write([]byte(strconv.Itoa(os.Getpid()))))
		})
		// Also report runners for procedure tests
		serveMux.HandleFunc("/_runners", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			var runners []string
			for name, _ := range handler.runners {
				runners = append(runners, name)
			}
			must.Get(w.Write(must.Get(json.Marshal(runners))))
		})
	}

	return &http.Server{
		Addr:    addr,
		Handler: serveMux,
	}
}
