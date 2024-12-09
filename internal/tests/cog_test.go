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
	"strings"
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

type UploadRequest struct {
	Method string
	Path   string
	Body   []byte
}

type WebhookHandler struct {
	mu              sync.Mutex
	webhookRequests []WebhookRequest
	uploadRequests  []UploadRequest
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := logger.Sugar()
	body := must.Get(io.ReadAll(r.Body))
	if strings.HasPrefix(r.URL.Path, "/webhook") {
		log.Infow("received webhook", "method", r.Method, "path", r.URL.Path)
		var resp server.PredictionResponse
		must.Do(json.Unmarshal(body, &resp))
		req := WebhookRequest{
			Method:   r.Method,
			Path:     r.URL.Path,
			Response: resp,
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.webhookRequests = append(h.webhookRequests, req)
	} else if strings.HasPrefix(r.URL.Path, "/upload") {
		log.Infow("received upload", "method", r.Method, "path", r.URL.Path)
		req := UploadRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.uploadRequests = append(h.uploadRequests, req)
	} else {
		log.Fatalw("received unknown request", "method", r.Method, "path", r.URL.Path)
	}
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
			if !strings.HasPrefix(req.Path, "/webhook") {
				continue
			}
			if req.Response.Status.IsCompleted() {
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
		if !strings.HasPrefix(req.Path, "/webhook") {
			continue
		}
		assert.Equal(ct.t, http.MethodPost, req.Method)
		r = append(r, req.Response)
	}
	return r
}

func (ct *CogTest) GetUploads() []UploadRequest {
	return ct.webhookServer.Handler.(*WebhookHandler).uploadRequests
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
	req := server.PredictionRequest{Input: input}
	return ct.prediction(http.MethodPost, ct.Url("/predictions"), req)
}

func (ct *CogTest) PredictionWithId(pid string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{Input: input}
	return ct.prediction(http.MethodPut, ct.Url(fmt.Sprintf("/predictions/%s", pid)), req)
}

func (ct *CogTest) PredictionWithUpload(input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{
		Input:            input,
		OutputFilePrefix: fmt.Sprintf("http://localhost:%d/upload/", ct.webhookPort),
	}
	return ct.prediction(http.MethodPost, ct.Url("/predictions"), req)
}

func (ct *CogTest) prediction(method string, url string, req server.PredictionRequest) server.PredictionResponse {
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
	req := server.PredictionRequest{Input: input}
	return ct.asyncPrediction(http.MethodPost, ct.Url("/predictions"), req)
}

func (ct *CogTest) AsyncPredictionWithFilter(input map[string]any, filter []server.WebhookEvent) string {
	req := server.PredictionRequest{
		Input:               input,
		WebhookEventsFilter: filter,
	}
	return ct.asyncPrediction(http.MethodPost, ct.Url("/predictions"), req)
}

func (ct *CogTest) AsyncPredictionWithId(pid string, input map[string]any) string {
	req := server.PredictionRequest{Input: input}
	return ct.asyncPrediction(http.MethodPut, ct.Url(fmt.Sprintf("/predictions/%s", pid)), req)
}

func (ct *CogTest) asyncPrediction(method string, url string, req server.PredictionRequest) string {
	ct.pending++
	req.Webhook = fmt.Sprintf("http://localhost:%d/webhook", ct.webhookPort)
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
