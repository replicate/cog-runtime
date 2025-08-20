package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

// This file implements the basis for the test harness. It is used to test the
// runtime server.

// Test-Suite Wide variables.
var (
	basePath       string
	legacyCog      *bool = new(bool)
	proceduresPath string
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

func (tr *testHarnessReceiver) webhookHandler(t *testing.T) http.HandlerFunc {
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

func (tr *testHarnessReceiver) uploadHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		body, err := io.ReadAll(r.Body)
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
	t.Cleanup(tr.Server.Close)
	return tr
}

type cogRuntimeServerConfig struct {
	procedureMode     bool
	explicitShutdown  bool
	uploadURL         string
	module            string
	predictorClass    string
	concurrencyMax    int
	concurrencyPerCPU int

	envSet   map[string]string
	envUnset []string
}

func (cfg *cogRuntimeServerConfig) validate(t *testing.T) {
	t.Helper()
	assert.NotEmpty(t, cfg.module)
	assert.NotEmpty(t, cfg.predictorClass)
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
	}
	concurrencyMax := max(cfg.concurrencyMax, 1)
	t.Logf("concurrency max: %d", concurrencyMax)

	if cfg.procedureMode && cfg.concurrencyPerCPU > 0 {
		t.Logf("concurrency per CPU: %d", cfg.concurrencyPerCPU)
	}

	writeCogConfig(t, tempDir, cfg.predictorClass, concurrencyMax)
	linkPythonModule(t, basePath, tempDir, cfg.module)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// NOTE(morgan): We now have the IPCUrl, so we can create the handler.
	// FIXME: This should be done over unix sockets instead of HTTP, it resolves
	// the chicken and egg problem of needing the IPCUrl to create the handler.
	handler, err := server.NewHandler(serverCfg, cancel)
	require.NoError(t, err)
	mux := server.NewServeMux(handler, serverCfg.UseProcedureMode)
	s.Config.Handler = mux

	// FIXME: This is a hack to cover shutdown logic that is expected. This
	// is more compatbility for the migration away from `cog_test`
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return s, handler
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

func httpPredictionRequest(t *testing.T, runtimeServer *httptest.Server, receiverServer *testHarnessReceiver, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	assert.Empty(t, prediction.Id)
	return httpPredictionReq(t, http.MethodPost, runtimeServer, receiverServer, prediction)
}

func httpPredictionRequestWithId(t *testing.T, runtimeServer *httptest.Server, receiverServer *testHarnessReceiver, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	assert.NotEmpty(t, prediction.Id)
	return httpPredictionReq(t, http.MethodPost, runtimeServer, receiverServer, prediction)
}

func httpPredictionReq(t *testing.T, method string, runtimeServer *httptest.Server, receiverServer *testHarnessReceiver, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	url := runtimeServer.URL + "/predictions"
	body, err := json.Marshal(prediction)
	require.NoError(t, err)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if receiverServer != nil && prediction.Webhook != "" {
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
