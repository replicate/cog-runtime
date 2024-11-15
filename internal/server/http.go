package server

import (
	"net/http"
)

func NewServer(addr string, runner *Runner) *http.Server {
	handler := Handler{runner: runner}
	serveMux := http.NewServeMux()
	serveMux.HandleFunc("GET /{$}", handler.Root)
	serveMux.HandleFunc("GET /health-check", handler.HealthCheck)
	serveMux.HandleFunc("GET /openapi.json", handler.OpenApi)
	serveMux.HandleFunc("POST /predictions", handler.Predict)
	serveMux.HandleFunc("PUT /predictions/{id}", handler.Predict)
	// POST /predictions/<pid>/cancel
	serveMux.HandleFunc("POST /shutdown", handler.Shutdown)

	return &http.Server{
		Addr:    addr,
		Handler: serveMux,
	}
}
