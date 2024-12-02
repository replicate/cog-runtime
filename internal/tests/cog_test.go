package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"testing"
	"time"

	"github.com/replicate/go/logging"

	"github.com/replicate/go/must"
	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

var (
	_, b, _, _ = runtime.Caller(0)
	basePath   = path.Dir(path.Dir(path.Dir(b)))
	logger     = logging.New("cog-test")
)

type WebhookRequest struct {
	Method   string
	Path     string
	Response server.PredictionResponse
}

type WebhookHandler struct {
	webhookRequests []WebhookRequest
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var resp server.PredictionResponse
	must.Do(json.Unmarshal(must.Get(io.ReadAll(r.Body)), &resp))
	req := WebhookRequest{
		Method:   r.Method,
		Path:     r.URL.Path,
		Response: resp,
	}
	log := logger.Sugar()
	log.Infow("webhook", "request", req)
	h.webhookRequests = append(h.webhookRequests, req)
}

var _ = (http.Handler)((*WebhookHandler)(nil))

type CogTest struct {
	t             *testing.T
	module        string
	extraArgs     []string
	extraEnvs     []string
	serverPort    int
	webhookPort   int
	cmd           *exec.Cmd
	webhookServer *http.Server
}

func NewCogTest(t *testing.T, module string) *CogTest {
	t.Parallel()
	return &CogTest{
		t:      t,
		module: module,
	}
}

func (e *CogTest) AppendArgs(args ...string) {
	e.extraArgs = append(e.extraArgs, args...)
}

func (e *CogTest) AppendEnvs(envs ...string) {
	e.extraEnvs = append(e.extraEnvs, envs...)
}

func (e *CogTest) StartWebhook() {
	e.webhookPort = getFreePort()
	e.webhookServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", e.webhookPort),
		Handler: &WebhookHandler{},
	}
	go func() {
		err := e.webhookServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()
}

func (e *CogTest) Start() error {
	pathEnv := path.Join(basePath, "python", ".venv", "bin")
	pythonPathEnv := path.Join(basePath, "python")
	e.serverPort = getFreePort()
	args := []string{
		"run", path.Join(basePath, "cmd", "cog-server", "main.go"),
		"--module-name", fmt.Sprintf("tests.runners.%s", e.module),
		"--class-name", "Predictor",
		"--port", fmt.Sprintf("%d", e.serverPort),
	}
	args = append(args, e.extraArgs...)
	e.cmd = exec.Command("go", args...)
	e.cmd.Env = os.Environ()
	e.cmd.Env = append(e.cmd.Env,
		fmt.Sprintf("PATH=%s:%s", pathEnv, os.Getenv("PATH")),
		fmt.Sprintf("PYTHONPATH=%s", pythonPathEnv),
	)
	e.cmd.Env = append(e.cmd.Env, e.extraEnvs...)
	e.cmd.Stdout = os.Stdout
	e.cmd.Stderr = os.Stderr
	return e.cmd.Start()
}

func (e *CogTest) Cleanup() error {
	if e.webhookServer != nil {
		must.Do(e.webhookServer.Shutdown(context.Background()))
	}
	return e.cmd.Wait()
}

func (e *CogTest) WebhookRequests() []WebhookRequest {
	return e.webhookServer.Handler.(*WebhookHandler).webhookRequests
}

func (e *CogTest) Url(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", e.serverPort, path)
}

func (e *CogTest) HealthCheck() server.HealthCheck {
	url := fmt.Sprintf("http://localhost:%d/health-check", e.serverPort)
	for {
		resp, err := http.DefaultClient.Get(url)
		if err == nil {
			var hc server.HealthCheck
			must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &hc))
			return hc
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *CogTest) WaitForSetup() server.HealthCheck {
	for {
		hc := e.HealthCheck()
		if hc.Status != "STARTING" {
			return hc
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *CogTest) Prediction(input map[string]any) server.PredictionResponse {
	return e.prediction(http.MethodPost, e.Url("/predictions"), input)
}

func (e *CogTest) PredictionWithId(pid string, input map[string]any) server.PredictionResponse {
	return e.prediction(http.MethodPut, e.Url(fmt.Sprintf("/predictions/%s", pid)), input)
}

func (e *CogTest) prediction(method string, url string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{Input: input}
	data := bytes.NewReader(must.Get(json.Marshal(req)))
	r := must.Get(http.NewRequest(method, url, data))
	r.Header.Set("Content-Type", "application/json")
	resp := must.Get(http.DefaultClient.Do(r))
	assert.Equal(e.t, http.StatusOK, resp.StatusCode)
	var pr server.PredictionResponse
	must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &pr))
	return pr
}

func (e *CogTest) AsyncPrediction(input map[string]any) {
	e.asyncPrediction(http.MethodPost, e.Url("/predictions"), input)
}

func (e *CogTest) AsyncPredictionWithId(pid string, input map[string]any) {
	e.asyncPrediction(http.MethodPut, e.Url(fmt.Sprintf("/predictions/%s", pid)), input)
}

func (e *CogTest) asyncPrediction(method string, url string, input map[string]any) {
	req := server.PredictionRequest{Input: input, Webhook: fmt.Sprintf("http://localhost:%d/webhook", e.webhookPort)}
	data := bytes.NewReader(must.Get(json.Marshal(req)))
	r := must.Get(http.NewRequest(method, url, data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Prefer", "respond-async")
	resp := must.Get(http.DefaultClient.Do(r))
	assert.Equal(e.t, http.StatusOK, resp.StatusCode)
}

func (e *CogTest) Shutdown() {
	url := fmt.Sprintf("http://localhost:%d/shutdown", e.serverPort)
	resp := must.Get(http.DefaultClient.Post(url, "", nil))
	assert.Equal(e.t, http.StatusOK, resp.StatusCode)
}

func getFreePort() int {
	a := must.Get(net.ResolveTCPAddr("tcp", "localhost:0"))
	l := must.Get(net.ListenTCP("tcp", a))
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
