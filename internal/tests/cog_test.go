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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

var (
	_, b, _, _ = runtime.Caller(0)
	basePath   = path.Dir(path.Dir(path.Dir(b)))
	logger     = util.CreateLogger("cog-test")
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
	body, err := io.ReadAll(r.Body)
	assert.NoError(h.ct.t, err)
	switch {
	case strings.HasPrefix(r.URL.Path, "/webhook"):
		log.Infow("received webhook", "method", r.Method, "path", r.URL.Path)
		var resp server.PredictionResponse
		err = json.Unmarshal(body, &resp)
		assert.NoError(h.ct.t, err)
		req := WebhookRequest{
			Method:   r.Method,
			Path:     r.URL.Path,
			Response: resp,
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.webhookRequests = append(h.webhookRequests, req)
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/upload"):
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
			h.ct.WebhookURL()
			w.Header().Set("Location", fmt.Sprintf("%s%s", h.ct.UploadURL(), filename))
		}
		w.WriteHeader(http.StatusAccepted)
	default:
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
	t.Helper()
	t.Parallel()
	return &CogTest{
		t:      t,
		module: module,
	}
}

func NewCogProcedureTest(t *testing.T) *CogTest {
	t.Helper()
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
	serverPort, err := util.FindPort()
	require.NoError(ct.t, err)
	ct.serverPort = serverPort
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
			"TEST_COG_PREDICTOR_NAME=Predictor",
		)
	}
	return cmd
}

func (ct *CogTest) legacyCmd() *exec.Cmd {
	tmpDir := ct.t.TempDir()
	var pythonBin string
	if ct.procedure {
		pythonBin = path.Join(basePath, ".venv-procedure", "bin", "python3")
	} else {
		pythonBin = path.Join(basePath, ".venv-legacy", "bin", "python3")
		runnersPath := path.Join(basePath, "python", "tests", "runners")
		module := fmt.Sprintf("%s.py", ct.module)
		yamlLines := []string{`predict: "predict.py:Predictor"`}
		if strings.HasPrefix(ct.module, "async_") {
			yamlLines = append(yamlLines, "concurrency:")
			yamlLines = append(yamlLines, "  max: 2")
		}
		yaml := strings.Join(yamlLines, "\n")
		err := os.WriteFile(path.Join(tmpDir, "cog.yaml"), []byte(yaml), 0644)
		require.NoError(ct.t, err)
		err = os.Symlink(path.Join(runnersPath, module), path.Join(tmpDir, "predict.py"))
		require.NoError(ct.t, err)
	}

	serverPort, err := util.FindPort()
	require.NoError(ct.t, err)
	ct.serverPort = serverPort
	args := []string{
		"-m", "cog.server.http",
	}
	args = append(args, ct.extraArgs...)
	cmd := exec.Command(pythonBin, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", ct.serverPort))
	cmd.Env = append(cmd.Env, "PYTHONUNBUFFERED=1")
	cmd.Env = append(cmd.Env, ct.extraEnvs...)
	return cmd
}

func (ct *CogTest) Cleanup() error {
	if ct.webhookServer != nil {
		err := ct.webhookServer.Shutdown(context.Background())
		require.NoError(ct.t, err)
	}
	return ct.cmd.Wait()
}

func (ct *CogTest) StartWebhook() {
	log := logger.Sugar()
	webhookPort, err := util.FindPort()
	require.NoError(ct.t, err)
	ct.webhookPort = webhookPort
	ct.webhookServer = &http.Server{
		Addr:        fmt.Sprintf(":%d", ct.webhookPort),
		Handler:     &WebhookHandler{ct: ct},
		ReadTimeout: 10 * time.Second,
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
		handler, _ := ct.webhookServer.Handler.(*WebhookHandler)
		for _, req := range handler.webhookRequests {
			if !strings.HasPrefix(req.Path, "/webhook") {
				continue
			}
			if fn(req.Response) {
				matches[req.Response.ID] = true
			}
		}
		if len(matches) == ct.pending {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var r []server.PredictionResponse
	handler, _ := ct.webhookServer.Handler.(*WebhookHandler)
	for _, req := range handler.webhookRequests {
		if !strings.HasPrefix(req.Path, "/webhook") {
			continue
		}
		assert.Equal(ct.t, http.MethodPost, req.Method)
		r = append(r, req.Response)
	}
	return r
}

func (ct *CogTest) GetUploads() []UploadRequest {
	handler, _ := ct.webhookServer.Handler.(*WebhookHandler)
	return handler.uploadRequests
}

func (ct *CogTest) URL(pathStr string) string {
	return fmt.Sprintf("http://localhost:%d%s", ct.serverPort, pathStr)
}

func (ct *CogTest) WebhookURL() string {
	return fmt.Sprintf("http://localhost:%d/webhook", ct.webhookPort)
}

func (ct *CogTest) UploadURL() string {
	return fmt.Sprintf("http://localhost:%d/upload/", ct.webhookPort)
}

func (ct *CogTest) ServerPid() int {
	if *legacyCog {
		return ct.cmd.Process.Pid
	}
	url := fmt.Sprintf("http://localhost:%d/_pid", ct.serverPort)
	resp, err := http.DefaultClient.Get(url)
	require.NoError(ct.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(ct.t, err)
	pid, err := strconv.Atoi(string(body))
	require.NoError(ct.t, err)
	return pid
}

func (ct *CogTest) Runners() []string {
	if *legacyCog {
		return nil
	}
	url := fmt.Sprintf("http://localhost:%d/_runners", ct.serverPort)
	resp, err := http.DefaultClient.Get(url)
	require.NoError(ct.t, err)
	defer resp.Body.Close()
	var runners []string
	body, err := io.ReadAll(resp.Body)
	require.NoError(ct.t, err)
	err = json.Unmarshal(body, &runners)
	require.NoError(ct.t, err)
	slices.Sort(runners)
	return runners

}

func (ct *CogTest) HealthCheck() server.HealthCheck {
	url := fmt.Sprintf("http://localhost:%d/health-check", ct.serverPort)
	for {
		resp, err := http.DefaultClient.Get(url)
		if err == nil {
			var hc server.HealthCheck
			body, err := io.ReadAll(resp.Body)
			require.NoError(ct.t, err)
			err = json.Unmarshal(body, &hc)
			require.NoError(ct.t, err)
			resp.Body.Close()
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

func (ct *CogTest) PredictionWithID(pid string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{ID: pid, Input: input}
	return ct.prediction(http.MethodPut, fmt.Sprintf("/predictions/%s", pid), req)
}

func (ct *CogTest) PredictionWithUpload(input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{
		Input:            input,
		OutputFilePrefix: ct.UploadURL(),
	}
	return ct.prediction(http.MethodPost, "/predictions", req)
}

func (ct *CogTest) AsyncPrediction(input map[string]any) string {
	req := server.PredictionRequest{
		Input:   input,
		Webhook: ct.WebhookURL(),
	}
	resp := ct.prediction(http.MethodPost, "/predictions", req)
	return resp.ID
}

func (ct *CogTest) AsyncPredictionWithFilter(input map[string]any, filter []server.WebhookEvent) string {
	req := server.PredictionRequest{
		Input:               input,
		Webhook:             ct.WebhookURL(),
		WebhookEventsFilter: filter,
	}
	resp := ct.prediction(http.MethodPost, "/predictions", req)
	return resp.ID
}

func (ct *CogTest) AsyncPredictionWithID(pid string, input map[string]any) string {
	req := server.PredictionRequest{
		ID:      pid,
		Input:   input,
		Webhook: ct.WebhookURL(),
	}
	resp := ct.prediction(http.MethodPut, fmt.Sprintf("/predictions/%s", pid), req)
	return resp.ID
}

func (ct *CogTest) prediction(method string, pathStr string, req server.PredictionRequest) server.PredictionResponse {
	resp := ct.PredictionReq(method, pathStr, req)
	defer resp.Body.Close()
	if req.Webhook == "" {
		assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
	} else {
		assert.Equal(ct.t, http.StatusAccepted, resp.StatusCode)
	}
	ct.pending++
	var pr server.PredictionResponse
	body, err := io.ReadAll(resp.Body)
	require.NoError(ct.t, err)
	err = json.Unmarshal(body, &pr)
	require.NoError(ct.t, err)
	return pr
}

func (ct *CogTest) PredictionReq(method string, pathStr string, req server.PredictionRequest) *http.Response {
	req.CreatedAt = util.NowIso()
	data, err := json.Marshal(req)
	require.NoError(ct.t, err)
	r, err := http.NewRequest(method, ct.URL(pathStr), bytes.NewReader(data))
	require.NoError(ct.t, err)
	r.Header.Set("Content-Type", "application/json")
	if req.Webhook != "" {
		r.Header.Set("Prefer", "respond-async")
	}
	resp, err := http.DefaultClient.Do(r)
	require.NoError(ct.t, err)
	return resp
}

func (ct *CogTest) Cancel(pid string) {
	url := ct.URL(fmt.Sprintf("/predictions/%s/cancel", pid))
	resp, err := http.DefaultClient.Post(url, "application/json", nil)
	require.NoError(ct.t, err)
	assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
}

func (ct *CogTest) Shutdown() {
	url := ct.URL("/shutdown")
	resp, err := http.DefaultClient.Post(url, "", nil)
	require.NoError(ct.t, err)
	assert.Equal(ct.t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
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
			assert.Contains(ct.t, finalLogs, r.Logs)
		} else {
			assert.Equal(ct.t, server.PredictionProcessing, r.Status)
			// Logs are incremental
			assert.Contains(ct.t, r.Logs, logs)
			logs = r.Logs
		}
	}
}
