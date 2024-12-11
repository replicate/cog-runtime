package server

import (
	"errors"
	"net/http"
)

var (
	ErrNotFound = errors.New("prediction ID not found")
)

func NewServer(addr string, runner *Runner) *http.Server {
	handler := Handler{runner: runner}
	serveMux := http.NewServeMux()
	serveMux.HandleFunc("GET /{$}", handler.Root)
	serveMux.HandleFunc("GET /health-check", handler.HealthCheck)
	serveMux.HandleFunc("GET /openapi.json", handler.OpenApi)
	serveMux.HandleFunc("POST /predictions", handler.Predict)
	serveMux.HandleFunc("PUT /predictions/{id}", handler.Predict)
	serveMux.HandleFunc("POST /predictions/{id}/cancel", handler.Cancel)
	serveMux.HandleFunc("POST /shutdown", handler.Shutdown)

	return &http.Server{
		Addr:    addr,
		Handler: serveMux,
	}
}
