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
	"sync"
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
	mu              sync.Mutex
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
	h.mu.Lock()
	defer h.mu.Unlock()
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
	pending       int
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

func (ct *CogTest) AppendArgs(args ...string) {
	ct.extraArgs = append(ct.extraArgs, args...)
}

func (ct *CogTest) AppendEnvs(envs ...string) {
	ct.extraEnvs = append(ct.extraEnvs, envs...)
}

func (ct *CogTest) Start() error {
	pathEnv := path.Join(basePath, "python", ".venv", "bin")
	pythonPathEnv := path.Join(basePath, "python")
	ct.serverPort = getFreePort()
	args := []string{
		"run", path.Join(basePath, "cmd", "cog-server", "main.go"),
		"--module-name", fmt.Sprintf("tests.runners.%s", ct.module),
		"--class-name", "Predictor",
		"--port", fmt.Sprintf("%d", ct.serverPort),
	}
	args = append(args, ct.extraArgs...)
	ct.cmd = exec.Command("go", args...)
	ct.cmd.Env = os.Environ()
	ct.cmd.Env = append(ct.cmd.Env,
		fmt.Sprintf("PATH=%s:%s", pathEnv, os.Getenv("PATH")),
		fmt.Sprintf("PYTHONPATH=%s", pythonPathEnv),
	)
	ct.cmd.Env = append(ct.cmd.Env, ct.extraEnvs...)
	ct.cmd.Stdout = os.Stdout
	ct.cmd.Stderr = os.Stderr
	return ct.cmd.Start()
}

func (ct *CogTest) Cleanup() error {
	if ct.webhookServer != nil {
		must.Do(ct.webhookServer.Shutdown(context.Background()))
	}
	return ct.cmd.Wait()
}

func (ct *CogTest) StartWebhook() {
	log := logger.Sugar()
	ct.webhookPort = getFreePort()
	ct.webhookServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", ct.webhookPort),
		Handler: &WebhookHandler{},
	}
	go func() {
		err := ct.webhookServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalw("failed to start webhook server", "error", err)
		}
	}()
}

func (ct *CogTest) WaitForWebhookResponses() []server.PredictionResponse {
	for {
		completed := make(map[string]bool)
		for _, req := range ct.webhookServer.Handler.(*WebhookHandler).webhookRequests {
			if _, ok := server.PredictionCompletedStatuses[req.Response.Status]; ok {
				completed[req.Response.Id] = true
			}
		}
		if len(completed) == ct.pending {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var r []server.PredictionResponse
	for _, req := range ct.webhookServer.Handler.(*WebhookHandler).webhookRequests {
		assert.Equal(ct.t, http.MethodPost, req.Method)
		assert.Equal(ct.t, "/webhook", req.Path)
		r = append(r, req.Response)
	}
	return r
}

func (ct *CogTest) Url(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", ct.serverPort, path)
}

func (ct *CogTest) HealthCheck() server.HealthCheck {
	url := fmt.Sprintf("http://localhost:%d/health-check", ct.serverPort)
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

func (ct *CogTest) WaitForSetup() server.HealthCheck {
	for {
		hc := ct.HealthCheck()
		if hc.Status != "STARTING" {
			return hc
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ct *CogTest) Prediction(input map[string]any) server.PredictionResponse {
	return ct.prediction(http.MethodPost, ct.Url("/predictions"), input)
}

func (ct *CogTest) PredictionWithId(pid string, input map[string]any) server.PredictionResponse {
	return ct.prediction(http.MethodPut, ct.Url(fmt.Sprintf("/predictions/%s", pid)), input)
}

func (ct *CogTest) prediction(method string, url string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{Input: input}
	data := bytes.NewReader(must.Get(json.Marshal(req)))
	r := must.Get(http.NewRequest(method, url, data))
	r.Header.Set("Content-Type", "application/json")
	resp := must.Get(http.DefaultClient.Do(r))
	assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
	var pr server.PredictionResponse
	must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &pr))
	return pr
}

func (ct *CogTest) AsyncPrediction(input map[string]any) string {
	return ct.asyncPrediction(http.MethodPost, ct.Url("/predictions"), input)
}

func (ct *CogTest) AsyncPredictionWithId(pid string, input map[string]any) string {
	return ct.asyncPrediction(http.MethodPut, ct.Url(fmt.Sprintf("/predictions/%s", pid)), input)
}

func (ct *CogTest) asyncPrediction(method string, url string, input map[string]any) string {
	ct.pending++
	req := server.PredictionRequest{Input: input, Webhook: fmt.Sprintf("http://localhost:%d/webhook", ct.webhookPort)}
	data := bytes.NewReader(must.Get(json.Marshal(req)))
	r := must.Get(http.NewRequest(method, url, data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Prefer", "respond-async")
	resp := must.Get(http.DefaultClient.Do(r))
	assert.Equal(ct.t, http.StatusAccepted, resp.StatusCode)
	var pr server.PredictionResponse
	must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &pr))
	return pr.Id
}

func (ct *CogTest) Shutdown() {
	url := fmt.Sprintf("http://localhost:%d/shutdown", ct.serverPort)
	resp := must.Get(http.DefaultClient.Post(url, "", nil))
	assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
}

func (ct *CogTest) AssertResponse(
	response server.PredictionResponse,
	status server.PredictionStatus,
	output any,
	logs string) {
	assert.Equal(ct.t, status, response.Status)
	assert.Equal(ct.t, output, response.Output)
	assert.Equal(ct.t, logs, response.Logs)
}

func getFreePort() int {
	a := must.Get(net.ResolveTCPAddr("tcp", "localhost:0"))
	l := must.Get(net.ListenTCP("tcp", a))
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
