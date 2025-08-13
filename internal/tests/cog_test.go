package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/go/logging"

	"github.com/replicate/go/must"
	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

var (
	_, b, _, _ = runtime.Caller(0)
	basePath   = path.Dir(path.Dir(path.Dir(b)))
	logger     = logging.New("cog-test")
	legacyCog  *bool
)

func init() {
	isLegacy, _ := strconv.ParseBool(os.Getenv("LEGACY_COG"))
	legacyCog = &isLegacy
}

type WebhookRequest struct {
	Method   string
	Path     string
	Response server.PredictionResponse
}

type UploadRequest struct {
	Method      string
	Path        string
	ContentType string
	Body        []byte
}

type WebhookHandler struct {
	ct              *CogTest
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
		w.WriteHeader(http.StatusOK)
	} else if strings.HasPrefix(r.URL.Path, "/upload") {
		log.Infow("received upload", "method", r.Method, "path", r.URL.Path)
		filename, ok := strings.CutPrefix(r.URL.Path, "/upload/")
		assert.True(h.ct.t, ok)
		req := UploadRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.uploadRequests = append(h.uploadRequests, req)
		if !*legacyCog {
			// Compat: legacy Cog only sets this when --upload-url is set
			pid := r.Header.Get("X-Prediction-Id")
			assert.NotEmpty(h.ct.t, pid)
			h.ct.WebhookUrl()
			w.Header().Set("Location", fmt.Sprintf("%s%s", h.ct.UploadUrl(), filename))
		}
		w.WriteHeader(http.StatusAccepted)
	} else {
		log.Fatalw("received unknown request", "method", r.Method, "path", r.URL.Path)
	}
}

var _ = (http.Handler)((*WebhookHandler)(nil))

type CogTest struct {
	t             *testing.T
	module        string
	procedure     bool
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

func NewCogProcedureTest(t *testing.T) *CogTest {
	// No parallel procedure test since they use the same temp source directory
	return &CogTest{
		t:         t,
		procedure: true,
	}
}

func (ct *CogTest) AppendArgs(args ...string) {
	ct.extraArgs = append(ct.extraArgs, args...)
}

func (ct *CogTest) AppendEnvs(envs ...string) {
	ct.extraEnvs = append(ct.extraEnvs, envs...)
}

func (ct *CogTest) Start() error {
	return ct.StartWithPipes(os.Stdout, os.Stderr)
}

func (ct *CogTest) StartWithPipes(stdout, stderr io.Writer) error {
	if *legacyCog {
		ct.cmd = ct.legacyCmd()
	} else {
		ct.cmd = ct.runtimeCmd()
	}
	ct.cmd.Stdout = stdout
	ct.cmd.Stderr = stderr
	return ct.cmd.Start()
}

func (ct *CogTest) runtimeCmd() *exec.Cmd {
	pathEnv := path.Join(basePath, ".venv", "bin")
	pythonPathEnv := path.Join(basePath, "python")
	ct.serverPort = util.FindPort()
	args := []string{
		"run", path.Join(basePath, "cmd", "cog", "main.go"), "server",
		"--port", fmt.Sprintf("%d", ct.serverPort),
	}
	args = append(args, ct.extraArgs...)
	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()

	cmd.Env = append(cmd.Env,
		"TEST_COG=1",
		fmt.Sprintf("PATH=%s:%s", pathEnv, os.Getenv("PATH")),
		fmt.Sprintf("PYTHONPATH=%s", pythonPathEnv),
	)
	cmd.Env = append(cmd.Env, ct.extraEnvs...)

	if ct.procedure {
		cmd.Args = append(cmd.Args, "--use-procedure-mode")
	} else {
		cmd.Env = append(cmd.Env,
			// Pass module and predictor to Runner, to avoid creating a one-off cog.yaml
			fmt.Sprintf("TEST_COG_MODULE_NAME=tests.runners.%s", ct.module),
			fmt.Sprintf("TEST_COG_PREDICTOR_NAME=Predictor"),
		)
	}
	return cmd
}

func (ct *CogTest) legacyCmd() *exec.Cmd {
	var tmpDir string
	var pythonBin string
	if ct.procedure {
		pythonBin = path.Join(basePath, ".venv-procedure", "bin", "python3")
		tmpDir = path.Join(basePath, "..", "pipelines-runtime")
	} else {
		pythonBin = path.Join(basePath, ".venv-legacy", "bin", "python3")
		tmpDir = ct.t.TempDir()
		runnersPath := path.Join(basePath, "python", "tests", "runners")
		module := fmt.Sprintf("%s.py", ct.module)
		yamlLines := []string{`predict: "predict.py:Predictor"`}
		if strings.HasPrefix(ct.module, "async_") {
			yamlLines = append(yamlLines, "concurrency:")
			yamlLines = append(yamlLines, "  max: 2")
		}
		yaml := strings.Join(yamlLines, "\n")
		must.Do(os.WriteFile(path.Join(tmpDir, "cog.yaml"), []byte(yaml), 0644))
		must.Do(os.Symlink(path.Join(runnersPath, module), path.Join(tmpDir, "predict.py")))
	}

	ct.serverPort = util.FindPort()
	args := []string{
		"-m", "cog.server.http",
	}
	args = append(args, ct.extraArgs...)
	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", ct.serverPort))
	cmd.Env = append(cmd.Env, "PYTHONUNBUFFERED=1")
	cmd.Env = append(cmd.Env, ct.extraEnvs...)
	cmd.Env = append(cmd.Env, "PROCEDURE_CACHE_PATH=/tmp/procedures")
	return cmd
}

func (ct *CogTest) Cleanup() error {
	if ct.webhookServer != nil {
		must.Do(ct.webhookServer.Shutdown(context.Background()))
	}
	return ct.cmd.Wait()
}

func (ct *CogTest) StartWebhook() {
	log := logger.Sugar()
	ct.webhookPort = util.FindPort()
	ct.webhookServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", ct.webhookPort),
		Handler: &WebhookHandler{ct: ct},
	}
	go func() {
		err := ct.webhookServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalw("failed to start webhook server", "error", err)
		}
	}()
}

func (ct *CogTest) WaitForWebhookCompletion() []server.PredictionResponse {
	return ct.WaitForWebhook(func(response server.PredictionResponse) bool { return response.Status.IsCompleted() })
}

func (ct *CogTest) WaitForWebhook(fn func(response server.PredictionResponse) bool) []server.PredictionResponse {
	for {
		matches := make(map[string]bool)
		for _, req := range ct.webhookServer.Handler.(*WebhookHandler).webhookRequests {
			if !strings.HasPrefix(req.Path, "/webhook") {
				continue
			}
			if fn(req.Response) {
				matches[req.Response.Id] = true
			}
		}
		if len(matches) == ct.pending {
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

func (ct *CogTest) WebhookUrl() string {
	return fmt.Sprintf("http://localhost:%d/webhook", ct.webhookPort)
}

func (ct *CogTest) UploadUrl() string {
	return fmt.Sprintf("http://localhost:%d/upload/", ct.webhookPort)
}

func (ct *CogTest) ServerPid() int {
	if *legacyCog {
		return ct.cmd.Process.Pid
	} else {
		url := fmt.Sprintf("http://localhost:%d/_pid", ct.serverPort)
		resp := must.Get(http.DefaultClient.Get(url))
		defer resp.Body.Close()
		return must.Get(strconv.Atoi(string(must.Get(io.ReadAll(resp.Body)))))
	}
}

func (ct *CogTest) Runners() []string {
	if *legacyCog {
		return nil
	} else {
		url := fmt.Sprintf("http://localhost:%d/_runners", ct.serverPort)
		resp := must.Get(http.DefaultClient.Get(url))
		defer resp.Body.Close()
		var runners []string
		must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &runners))
		slices.Sort(runners)
		return runners
	}
}

func (ct *CogTest) HealthCheck() server.HealthCheck {
	url := fmt.Sprintf("http://localhost:%d/health-check", ct.serverPort)
	for {
		resp, err := http.DefaultClient.Get(url)
		if err == nil {
			defer resp.Body.Close()
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
	return ct.prediction(http.MethodPost, "/predictions", req)
}

func (ct *CogTest) PredictionWithId(pid string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{Id: pid, Input: input}
	return ct.prediction(http.MethodPut, fmt.Sprintf("/predictions/%s", pid), req)
}

func (ct *CogTest) PredictionWithUpload(input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{
		Input:            input,
		OutputFilePrefix: ct.UploadUrl(),
	}
	return ct.prediction(http.MethodPost, "/predictions", req)
}

func (ct *CogTest) AsyncPrediction(input map[string]any) string {
	req := server.PredictionRequest{
		Input:   input,
		Webhook: ct.WebhookUrl(),
	}
	resp := ct.prediction(http.MethodPost, "/predictions", req)
	return resp.Id
}

func (ct *CogTest) AsyncPredictionWithFilter(input map[string]any, filter []server.WebhookEvent) string {
	req := server.PredictionRequest{
		Input:               input,
		Webhook:             ct.WebhookUrl(),
		WebhookEventsFilter: filter,
	}
	resp := ct.prediction(http.MethodPost, "/predictions", req)
	return resp.Id
}

func (ct *CogTest) AsyncPredictionWithId(pid string, input map[string]any) string {
	req := server.PredictionRequest{
		Id:      pid,
		Input:   input,
		Webhook: ct.WebhookUrl(),
	}
	resp := ct.prediction(http.MethodPut, fmt.Sprintf("/predictions/%s", pid), req)
	return resp.Id
}

func (ct *CogTest) prediction(method string, path string, req server.PredictionRequest) server.PredictionResponse {
	resp := ct.PredictionReq(method, path, req)
	if req.Webhook == "" {
		assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
	} else {
		assert.Equal(ct.t, http.StatusAccepted, resp.StatusCode)
	}
	ct.pending++
	var pr server.PredictionResponse
	must.Do(json.Unmarshal(must.Get(io.ReadAll(resp.Body)), &pr))
	return pr
}

func (ct *CogTest) PredictionReq(method string, path string, req server.PredictionRequest) *http.Response {
	req.CreatedAt = util.NowIso()
	data := bytes.NewReader(must.Get(json.Marshal(req)))
	r := must.Get(http.NewRequest(method, ct.Url(path), data))
	r.Header.Set("Content-Type", "application/json")
	if req.Webhook != "" {
		r.Header.Set("Prefer", "respond-async")
	}
	return must.Get(http.DefaultClient.Do(r))
}

func (ct *CogTest) Cancel(pid string) {
	url := ct.Url(fmt.Sprintf("/predictions/%s/cancel", pid))
	resp := must.Get(http.DefaultClient.Post(url, "application/json", nil))
	assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
}

func (ct *CogTest) Shutdown() {
	url := ct.Url("/shutdown")
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

func (ct *CogTest) AssertResponses(
	responses []server.PredictionResponse,
	finalStatus server.PredictionStatus,
	finalOutput any,
	finalLogs string) {
	l := len(responses)
	logs := ""
	for i, r := range responses {
		if i == l-1 {
			assert.Equal(ct.t, finalStatus, r.Status)
			assert.Equal(ct.t, finalOutput, r.Output)
			assert.Contains(ct.t, r.Logs, finalLogs)
		} else {
			assert.Equal(ct.t, r.Status, server.PredictionProcessing)
			// Logs are incremental
			assert.Contains(ct.t, r.Logs, logs)
			logs = r.Logs
		}
	}
}
