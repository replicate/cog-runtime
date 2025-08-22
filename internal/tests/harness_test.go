package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
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
)

// This file implements the basis for the test harness. It is used to test the
// runtime server.

const (
	procedureFilePathURITemplate = "file://%s/python/tests/procedures/%s"
)

// Test-Suite Wide variables.
var (
	basePath       string
	legacyCog      *bool = new(bool)
	proceduresPath string

	portMatchRegex = regexp.MustCompile(`http://[^:]+:(\d+)`)
)

type webhookData struct {
	Method   string
	Path     string
	Response server.PredictionResponse
}

type uploadData struct {
	Method      string
	Path        string
	ContentType string
	Body        []byte
}

type testHarnessReceiver struct {
	*httptest.Server

	mu              sync.Mutex
	webhookRequests []webhookData
	uploadRequests  []uploadData

	webhookReceiverChan chan webhookData
	uploadReceiverChan  chan uploadData
}

func (tr *testHarnessReceiver) webhookHandler(t *testing.T) http.HandlerFunc { //nolint:thelper // this wont be called directly via test, it is called as a webhook receiver
	return func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, http.MethodPost, r.Method)
		var resp server.PredictionResponse
		err = json.Unmarshal(body, &resp)
		assert.NoError(t, err)
		message := webhookData{
			Method:   r.Method,
			Path:     r.URL.Path,
			Response: resp,
		}
		tr.webhookRequests = append(tr.webhookRequests, message)
		tr.webhookReceiverChan <- message
	}
}

func (tr *testHarnessReceiver) uploadHandler(t *testing.T) http.HandlerFunc { //nolint:thelper // this wont be called directly via test, it is called as a upload receiver
	return func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		// NOTE: Assertions here are to catch cases where the uploader does the wrong thing,
		// as we may or may not be able to catch an error in the upload from functional-style
		// testing, this can start going away once we migrate (where appropriate) to using the
		// unit-tests.
		assert.NoError(t, err)
		assert.True(t, slices.Contains([]string{http.MethodPut, http.MethodPost}, r.Method))
		message := uploadData{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		}
		tr.uploadRequests = append(tr.uploadRequests, message)
		tr.uploadReceiverChan <- message
	}
}

func testHarnessReceiverServer(t *testing.T) *testHarnessReceiver {
	t.Helper()
	tr := &testHarnessReceiver{}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", tr.webhookHandler(t))
	mux.HandleFunc("/upload/{filename}", tr.uploadHandler(t))
	// NOTE: buffered channels are used here to prevent issues arising from the handler
	// blocking while holding the lock. ~10 should be enough for the synthetic/small number
	// of requests in testing. Increase if needed. This allows the test to determine if it
	// wants to read from the channel or introspect the slice.
	tr.webhookReceiverChan = make(chan webhookData, 10)
	tr.uploadReceiverChan = make(chan uploadData, 10)
	tr.Server = httptest.NewServer(mux)
	t.Cleanup(tr.Close) // this is the same as tr.Server.Close()
	return tr
}

type cogRuntimeServerConfig struct {
	procedureMode    bool
	explicitShutdown bool
	uploadURL        string
	module           string
	predictorClass   string
	concurrencyMax   int
	maxRunners       int

	envSet   map[string]string
	envUnset []string
}

func (cfg *cogRuntimeServerConfig) validate(t *testing.T) {
	t.Helper()
	if !cfg.procedureMode {
		assert.NotEmpty(t, cfg.module)
		assert.NotEmpty(t, cfg.predictorClass)
	}
}

// setupCogRuntime is a convenience function that returns the server without the handler
func setupCogRuntime(t *testing.T, cfg cogRuntimeServerConfig) *httptest.Server {
	t.Helper()
	s, _ := setupCogRuntimeServer(t, cfg)
	return s
}

func setupCogRuntimeServer(t *testing.T, cfg cogRuntimeServerConfig) (*httptest.Server, *server.Handler) {
	t.Helper()
	cfg.validate(t)
	tempDir := t.TempDir()
	if cfg.procedureMode {
		t.Logf("procedure mode")
	}
	t.Logf("Working directory: %s", tempDir)
	// FIXME: This is for compatibility with the `cog_test` test harness while we migrate to in-process testing. This allows us
	// to specify the python venvs and binary in the same way as for minimizing the blast radius of changes.

	var pathEnv string

	// SetupEnvs for downstream use
	switch {
	case *legacyCog && cfg.procedureMode:
		pathEnv = path.Join(basePath, ".venv-procedure", "bin")
		t.Logf("using legacy Cog with venv: %s", pathEnv)
	case *legacyCog:
		pathEnv = path.Join(basePath, ".venv-legacy", "bin")
		t.Logf("using legacy Cog with venv: %s", pathEnv)
	default:
		pathEnv = path.Join(basePath, ".venv", "bin")
		t.Logf("using cog with venv: %s", pathEnv)
	}

	pythonPathEnv := path.Join(basePath, "python")

	// NOTE(morgan): this is a special case, we need the IPCUrl which is homed on the server before we create the handler. Create a nil
	// handler server and then set the handler after.
	s := httptest.NewServer(nil)
	t.Cleanup(s.Close)

	envSet := map[string]string{
		"PATH":       fmt.Sprintf("%s:%s", pathEnv, os.Getenv("PATH")),
		"PYTHONPATH": pythonPathEnv,
	}
	for k, v := range cfg.envSet {
		envSet[k] = v
	}

	serverCfg := server.Config{
		UseProcedureMode:      cfg.procedureMode,
		AwaitExplicitShutdown: cfg.explicitShutdown,
		UploadUrl:             cfg.uploadURL,
		WorkingDirectory:      tempDir,
		IPCUrl:                s.URL + "/_ipc",
		EnvSet:                envSet,
		EnvUnset:              cfg.envUnset,
		PythonBinPath:         path.Join(pathEnv, "python3"),
		MaxRunners:            cfg.maxRunners,
	}
	concurrencyMax := max(cfg.concurrencyMax, 1)
	t.Logf("concurrency max: %d", concurrencyMax)

	if cfg.procedureMode {
		if cfg.maxRunners > 0 {
			t.Logf("max runners: %d", cfg.maxRunners)
		} else {
			t.Logf("max runners: %d (default)", runtime.NumCPU()*4)
		}
	}

	if !cfg.procedureMode {
		writeCogConfig(t, tempDir, cfg.predictorClass, concurrencyMax)
		linkPythonModule(t, basePath, tempDir, cfg.module)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// FIXME: This is a hack to cover shutdown logic that is expected. This
	// is more compatbility for the migration away from `cog_test`
	go func() {
		<-ctx.Done()
		s.Close()
	}()

	// NOTE(morgan): We now have the IPCUrl, so we can create the handler.
	// FIXME: This should be done over unix sockets instead of HTTP, it resolves
	// the chicken and egg problem of needing the IPCUrl to create the handler.
	if *legacyCog {
		// Setup the legacy cog server wrapped in a http.ReverseProxy
		// this is just python cog running, this also means that the returned
		// handler is nil since it doesn't really exist as the "handler" object
		// we wire into the serveMux, this means procedure mode doesn't work under
		// legacy cog.
		if cfg.procedureMode {
			t.Fatalf("procedure mode is not supported under legacy cog")
		}
		environ := []string{
			fmt.Sprintf("PATH=%s", envSet["PATH"]),
		}
		port, err := startLegacyCogServer(t, ctx, path.Join(pathEnv, "python3"), tempDir, environ, cfg.uploadURL)
		require.NoError(t, err)
		target, _ := url.Parse(fmt.Sprintf("http://localhost:%d", port))
		handler := httputil.NewSingleHostReverseProxy(target)

		s.Config.Handler = handler
		return s, nil
	}
	// In non-Legacy cog mode we create the go-handler
	handler, err := server.NewHandler(serverCfg, cancel)
	require.NoError(t, err)
	mux := server.NewServeMux(handler, serverCfg.UseProcedureMode)
	s.Config.Handler = mux

	return s, handler
}

func startLegacyCogServer(t *testing.T, ctx context.Context, pythonPath string, tempDir string, environ []string, uploadUrl string) (int, error) {
	t.Helper()
	args := []string{"-m", "cog.server.http"}
	if uploadUrl != "" {
		args = append(args, fmt.Sprintf("--upload-url=%s", uploadUrl))
	}

	cmd := exec.CommandContext(ctx, pythonPath, args...)
	cmd.Dir = tempDir
	cmd.Env = environ

	for _, env := range environ {
		if strings.HasPrefix(env, "PORT=") {
			t.Fatalf("PORT environment variable may not be set when starting legacy cog server")
		}
	}
	cmd.Env = append(cmd.Env, "PORT=0", "PYTHONUNBUFFERED=1", "COG_LOG_LEVEL=DEBUG")
	stdErrLogs, err := cmd.StderrPipe()
	require.NoError(t, err)
	err = cmd.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		stdErrLogs.Close()
		cmd.Process.Kill()
	})

	// We need to do some lifting here to get the port from the logs
	type portResult struct {
		port int
		err  error
	}
	portChan := make(chan portResult, 1)
	go func() {
		port, err := parseLegacyCogServerLogsForPort(t, stdErrLogs)
		if err != nil {
			portChan <- portResult{port: -1, err: err}
			return
		}
		portChan <- portResult{port: port, err: nil}
		// discard the rest of the logs
		io.Copy(io.Discard, stdErrLogs)
	}()

	var port int
	select {
	case result := <-portChan:
		require.NoError(t, result.err, "failed to parse port from legacy cog server logs")
		port = result.port
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout scanning port from legacy cog server logs")
	}
	return port, nil
}

func parseLegacyCogServerLogsForPort(t *testing.T, logs io.ReadCloser) (int, error) {
	t.Helper()
	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Uvicorn running on") {
			matches := portMatchRegex.FindStringSubmatch(line)
			if len(matches) > 0 {
				port, err := strconv.Atoi(matches[1])
				if err != nil {
					return 0, err
				}
				t.Logf("cog server running on port: %d", port)
				return port, nil
			}
		}
	}
	t.Fatalf("could not find port in logs")
	return 0, fmt.Errorf("could not find port in logs")
}

type cogConfig struct {
	Predict     string `json:"predict"`
	Concurrency struct {
		Max int `json:"max"`
	} `json:"concurrency,omitempty"`
}

// writeCogConfig creates a cog.yaml file that contains json-ified version of the config.
// As JSON is a strict subset of YAML, this allows us to stdlib instead of needing external
// yaml-specific dependencies for a very basic cog.yaml
func writeCogConfig(t *testing.T, tempDir string, predictorClass string, concurrencyMax int) {
	t.Helper()
	conf := cogConfig{
		Predict: "predict.py:" + predictorClass,
	}
	if concurrencyMax > 0 {
		conf.Concurrency = struct {
			Max int `json:"max"`
		}{Max: concurrencyMax}
	}
	cogConfigFilePath := path.Join(tempDir, "cog.yaml")
	cogConfigFile, err := os.OpenFile(cogConfigFilePath, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	err = json.NewEncoder(cogConfigFile).Encode(conf)
	require.NoError(t, err)
}

// linkPythonModule links the python module into the temp directory.
// FIXME: this is a hack to provide compatibility with the `cog_test` test harness while we migrate to in-process testing.
func linkPythonModule(t *testing.T, basePath string, tempDir string, module string) {
	t.Helper()
	runnersPath := path.Join(basePath, "python", "tests", "runners")
	err := os.Symlink(path.Join(runnersPath, fmt.Sprintf("%s.py", module)), path.Join(tempDir, "predict.py"))
	require.NoError(t, err)
}

func healthCheck(t *testing.T, testServer *httptest.Server) server.HealthCheck {
	t.Helper()
	url := testServer.URL + "/health-check"
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var hc server.HealthCheck
	err = json.Unmarshal(body, &hc)
	require.NoError(t, err)
	return hc
}

func waitForSetupComplete(t *testing.T, testServer *httptest.Server, expectedStatus server.Status, expectedSetupStatus server.SetupStatus) server.HealthCheck {
	t.Helper()

	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()

	for range timer.C {
		hc := healthCheck(t, testServer)
		if hc.Status != server.StatusStarting.String() {
			assert.Equal(t, expectedStatus.String(), hc.Status)
			assert.Equal(t, expectedSetupStatus, hc.Setup.Status)
			return hc
		}
	}
	return server.HealthCheck{}
}

func httpPredictionRequest(t *testing.T, runtimeServer *httptest.Server, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	assert.Empty(t, prediction.Id)
	return httpPredictionReq(t, http.MethodPost, runtimeServer, prediction)
}

func httpPredictionRequestWithId(t *testing.T, runtimeServer *httptest.Server, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	assert.NotEmpty(t, prediction.Id)
	return httpPredictionReq(t, http.MethodPost, runtimeServer, prediction)
}

func httpPredictionReq(t *testing.T, method string, runtimeServer *httptest.Server, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	if prediction.CreatedAt != "" {
		t.Logf("using existing created_at: %s", prediction.CreatedAt)
		// verify that created_at is a valid time
		_, err := time.Parse(time.RFC3339, prediction.CreatedAt)
		require.NoError(t, err)
	}
	prediction.CreatedAt = time.Now().Format(time.RFC3339)

	url := runtimeServer.URL + "/predictions"
	body, err := json.Marshal(prediction)
	require.NoError(t, err)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if prediction.Webhook != "" {
		req.Header.Set("Prefer", "respond-async")
	}
	return req
}

func TestMain(m *testing.M) {
	_, b, _, _ := runtime.Caller(0)
	basePath = path.Dir(path.Dir(path.Dir(b)))
	isLegacy, err := strconv.ParseBool(os.Getenv("LEGACY_COG"))
	if err == nil {
		legacyCog = &isLegacy
	}
	proceduresPath = path.Join(basePath, "python", "tests", "procedures")
	os.Exit(m.Run())
}

// safeCloseChannel is a helper function to close a channel only if it is not already closed.
// it assumes that a single goroutine owns closing the channel and should only be used in
// the test harness in that scenario.
func safeCloseChannel(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}
