package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/replicate/cog-runtime/internal/util"
)

var LogRegex = regexp.MustCompile(`^\[pid=(?P<pid>[^]]+)] (?P<msg>.*)$`)
var ResponseRegex = regexp.MustCompile(`^response-(?P<pid>\S+)-(?P<epoch>\d+).json$`)
var CancelFmt = "cancel-%s"

type PendingPrediction struct {
	request     PredictionRequest
	response    PredictionResponse
	lastUpdated time.Time
	inputPaths  []string
	outputCache map[string]string
	mu          sync.Mutex
	c           chan PredictionResponse
}

func (pr *PendingPrediction) appendLogLine(line string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.response.Logs += fmt.Sprintln(line)
}

func (pr *PendingPrediction) sendWebhook(event WebhookEvent) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.request.Webhook == "" {
		return
	}
	if len(pr.request.WebhookEventsFilter) > 0 && !slices.Contains(pr.request.WebhookEventsFilter, event) {
		return
	}
	if event == WebhookLogs || event == WebhookOutput {
		if time.Since(pr.lastUpdated) < 500*time.Millisecond {
			return
		}
		pr.lastUpdated = time.Now()
	}

	log := logger.Sugar()
	log.Debugw("sending webhook", "url", pr.request.Webhook, "response", pr.response)
	if err := SendWebhook(pr.request.Webhook, &pr.response); err != nil {
		log.Errorw("failed to send webhook", "url", "error", err)
	}
}

func (pr *PendingPrediction) sendResponse() {
	if pr.c == nil {
		return
	}
	pr.c <- pr.response
}

type Runner struct {
	name           string
	workingDir     string
	tmpDir         string // temp directory for process isolation
	cmd            exec.Cmd
	status         Status
	schema         string
	doc            *openapi3.T
	setupResult    SetupResult
	logs           []string
	asyncPredict   bool
	maxConcurrency int
	pending        map[string]*PendingPrediction
	uploadUrl      string
	mu             sync.Mutex
	stopped        chan bool
}

const DefaultRunnerId = 0
const DefaultRunnerName = "default"

func NewRunner(name, cwd string, cfg Config) (*Runner, error) {
	// Ensure we default to the default path based python3 binary
	pythonBinPath := "python3"
	if cfg.PythonBinPath != "" {
		pythonBinPath = cfg.PythonBinPath
	}

	workingDir, err := os.MkdirTemp("", "cog-runner-")
	if err != nil {
		return nil, fmt.Errorf("failed to create working directory: %w", err)
	}
	args := []string{
		"-u",
		"-m", "coglet",
		"--name", name,
		"--ipc-url", cfg.IPCUrl,
		"--working-dir", workingDir,
	}
	cmd := exec.Command(pythonBinPath, args...)
	cmd.Dir = cwd

	cmd.Env = mergeEnv(os.Environ(), cfg.EnvSet, cfg.EnvUnset)

	return &Runner{
		name:           name,
		workingDir:     workingDir,
		cmd:            *cmd,
		status:         StatusStarting,
		maxConcurrency: 1,
		pending:        make(map[string]*PendingPrediction),
		uploadUrl:      cfg.UploadUrl,
		stopped:        make(chan bool),
	}, nil
}

func NewProcedureRunner(name, srcDir string, cfg Config) (*Runner, error) {
	r, err := NewRunner(name, srcDir, cfg)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Runner) String() string {
	return r.name
}

func (r *Runner) Start() error {
	log := logger.Sugar()
	cmdStart := make(chan bool)
	if err := r.setupLogging(cmdStart); err != nil {
		log.Errorw("failed to setup logging", "error", err)
		return err
	}
	// Placeholder in case setup crashes
	r.setupResult = SetupResult{
		StartedAt: util.NowIso(),
	}
	if err := r.cmd.Start(); err != nil {
		log.Errorw("failed to start command", "error", err)
		return err
	}
	log.Infow("python runner started", "pid", r.cmd.Process.Pid)
	close(cmdStart)
	go r.config()
	go r.wait()
	return nil
}

func (r *Runner) Stop() error {
	log := logger.Sugar()
	log.Infow("stop requested")

	// Clean up temp directory if it exists
	if r.tmpDir != "" {
		log.Infow("cleaning up temp directory", "tmpDir", r.tmpDir)
		if err := os.RemoveAll(r.tmpDir); err != nil {
			log.Errorw("failed to clean up temp directory", "tmpDir", r.tmpDir, "error", err)
		}
	}

	if r.cmd.ProcessState != nil {
		// Python process already exited
		// Shutdown HTTP server
		return nil
	} else {
		// Otherwise signal Python process to stop
		// FIXME: kill process after grace period
		p := path.Join(r.workingDir, "stop")
		return os.WriteFile(p, []byte{}, 0644)
	}
}

func (r *Runner) ExitCode() int {
	return r.cmd.ProcessState.ExitCode()
}

func (r *Runner) WaitForStop() {
	<-r.stopped
}

////////////////////
// Status

func (r *Runner) SrcDir() string {
	return r.cmd.Dir
}

func (r *Runner) Concurrency() Concurrency {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Concurrency{
		Max:     r.maxConcurrency,
		Current: len(r.pending),
	}
}

func (r *Runner) Idle() bool {
	// IPC from Python runner is the source of truth for Runner.status where
	// * Ready: pending predictions < max concurrency
	// * Busy: pending predictions = max concurrency
	// However, only runners with 0 pending predictions can be evicted in procedure mode
	return len(r.pending) == 0
}

// SetTmpDir sets the temp directory for testing purposes
func (r *Runner) SetTmpDir(tmpDir string) {
	r.tmpDir = tmpDir
}

////////////////////
// Prediction

func (r *Runner) Predict(req PredictionRequest) (chan PredictionResponse, error) {
	log := logger.Sugar()
	if r.status == StatusSetupFailed {
		log.Errorw("prediction rejected: setup failed")
		return nil, ErrSetupFailed
	} else if r.status == StatusDefunct {
		log.Errorw("prediction rejected: server is defunct")
		return nil, ErrDefunct
	}
	r.mu.Lock()
	if len(r.pending) >= r.maxConcurrency {
		r.mu.Unlock()
		log.Errorw("prediction rejected: Already running a prediction")
		return nil, ErrConflict
	}
	if _, ok := r.pending[req.Id]; ok {
		r.mu.Unlock()
		log.Errorw("prediction rejected: prediction exists", "id", req.Id)
		return nil, ErrExists
	}
	r.mu.Unlock()

	log.Infow("received prediction request", "id", req.Id)
	if req.CreatedAt == "" {
		req.CreatedAt = util.NowIso()
	}
	// Start here so that input downloads are counted towards predict_time
	if req.StartedAt == "" {
		req.StartedAt = util.NowIso()
	}

	inputPaths := make([]string, 0)
	input, err := handleInputPaths(req.Input, r.doc, &inputPaths, base64ToInput)
	if err != nil {
		return nil, err
	}
	input, err = handleInputPaths(req.Input, r.doc, &inputPaths, urlToInput)
	if err != nil {
		return nil, err
	}
	req.Input = input

	reqPath := path.Join(r.workingDir, fmt.Sprintf("request-%s.json", req.Id))
	bs, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	if err := os.WriteFile(reqPath, bs, 0644); err != nil {
		return nil, err
	}
	resp := PredictionResponse{
		Input:     req.Input,
		Id:        req.Id,
		CreatedAt: req.CreatedAt,
		StartedAt: req.StartedAt,
	}
	pr := PendingPrediction{
		request:     req,
		response:    resp,
		inputPaths:  inputPaths,
		outputCache: make(map[string]string),
	}
	if req.Webhook == "" {
		pr.c = make(chan PredictionResponse, 1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[req.Id] = &pr
	return pr.c, nil
}

func (r *Runner) Cancel(pid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[pid]; !ok {
		return ErrNotFound
	}
	if r.asyncPredict {
		// Async predict, use files to cancel
		p := path.Join(r.workingDir, fmt.Sprintf(CancelFmt, pid))
		return os.WriteFile(p, []byte{}, 0644)
	} else {
		// Blocking predict, use SIGUSR1 to cancel
		// FIXME: ensure only one prediction in flight?
		return syscall.Kill(r.cmd.Process.Pid, syscall.SIGUSR1)
	}
}

////////////////////
// Background tasks

func (r *Runner) config() error {
	log := logger.Sugar()

	// Wait until user files become available and pass config to Python runner
	waitFile := os.Getenv("COG_WAIT_FILE")
	if waitFile != "" {
		started := time.Now()
		timeout := 60 * time.Second
		found := false
		for time.Since(started) < timeout {
			if _, err := os.Stat(waitFile); err == nil {
				found = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !found {
			elapsed := time.Since(started)
			log.Errorw(
				"wait file not found after timeout", "elapsed", elapsed, "wait_file", waitFile)
			panic(fmt.Errorf("wait file not found after timeout %s: %s", elapsed, waitFile))
		}
	}

	// For testing only, set by CogTest, to avoid creating a one-off cog.yaml
	moduleName := os.Getenv("TEST_COG_MODULE_NAME")
	predictorName := os.Getenv("TEST_COG_PREDICTOR_NAME")
	maxConcurrencyStr := os.Getenv("TEST_COG_MAX_CONCURRENCY")
	if maxConcurrencyStr != "" {
		maxConcurrency, err := strconv.Atoi(maxConcurrencyStr)
		if err != nil {
			log.Errorw("failed to parse max concurrency, defaulting to 1", "error", err)
			maxConcurrency = 1
		}
		r.maxConcurrency = maxConcurrency
	}

	// Otherwise read from cog.yaml
	if moduleName == "" || predictorName == "" {
		y, err := util.ReadCogYaml(r.SrcDir())
		if err != nil {
			log.Errorw("failed to read cog.yaml", "path", r.SrcDir(), "error", err)
			panic(err)
		}
		m, c, err := y.PredictModuleAndPredictor()
		if err != nil {
			log.Errorw("failed to parse predict", "error", err)
			panic(err)
		}
		moduleName = m
		predictorName = c
		// Default to 1 if not set in cog.yaml, regardless whether async predict or not
		r.maxConcurrency = max(1, y.Concurrency.Max)

		// Send metrics for normal single instance runner
		// Do not send for multi-tenant procedure runners to reduce noise
		if r.name == DefaultRunnerName {
			go util.SendRunnerMetric(*y)
		}
	}
	conf := PredictConfig{
		ModuleName:     moduleName,
		PredictorName:  predictorName,
		MaxConcurrency: r.maxConcurrency,
	}
	log.Infow("configuring runner", "module", moduleName, "predictor", predictorName, "max_concurrency", r.maxConcurrency)
	confFile := path.Join(r.workingDir, "config.json")
	f, err := os.Create(confFile)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	if err := json.NewEncoder(f).Encode(conf); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	return nil
}

func (r *Runner) wait() {
	log := logger.Sugar()
	err := r.cmd.Wait()
	if err != nil {
		runnerLogs := r.rotateLogs()
		log.Errorw("python runner exited with error", "pid", r.cmd.Process.Pid, "error", err)
		for _, pr := range r.pending {
			pr.mu.Lock()
			now := util.NowIso()
			if pr.response.StartedAt == "" {
				pr.response.StartedAt = now
			}
			pr.response.CompletedAt = now
			pr.response.Logs += runnerLogs
			pr.response.Error = "prediction failed"
			pr.response.Status = PredictionFailed
			pr.mu.Unlock()

			pr.sendWebhook(WebhookCompleted)
			pr.sendResponse()
		}
		r.mu.Lock()
		if r.status == StatusStarting {
			r.status = StatusSetupFailed
			r.setupResult.CompletedAt = util.NowIso()
			r.setupResult.Status = SetupFailed
			r.setupResult.Logs = runnerLogs
		} else {
			r.status = StatusDefunct
		}
		r.mu.Unlock()
	} else {
		log.Infow("python runner exited successfully", "pid", r.cmd.Process.Pid)
		r.mu.Lock()
		r.status = StatusDefunct
		r.mu.Unlock()
	}
	close(r.stopped)
}

////////////////////
// IO handling

func (r *Runner) HandleIPC(s IPCStatus) error {
	log := logger.Sugar()
	switch s {
	case IPCStatusReady:
		if r.status == StatusStarting {
			r.updateSchema()
			r.updateSetupResult()
			if _, err := os.Stat(path.Join(r.workingDir, "async_predict")); err == nil {
				r.asyncPredict = true
			} else if errors.Is(err, os.ErrNotExist) && r.maxConcurrency > 1 {
				log.Warnw("max concurrency > 1 for blocking predict, reset to 1", "max_concurrency", r.maxConcurrency)
				r.maxConcurrency = 1
			}
			if err := writeReadyFile(); err != nil {
				log.Errorw("fail to write ready file", "error", err)
			}
		}
		log.Info("runner is ready")
		r.mu.Lock()
		r.status = StatusReady
		r.mu.Unlock()
	case IPCStatusBUSY:
		log.Info("runner is busy")
		r.mu.Lock()
		r.status = StatusBusy
		r.mu.Unlock()
	case IPCStatusOutput:
		if err := r.handleResponses(); err != nil {
			log.Errorw("failed to handle responses", "error", err)
			return err
		}
	default:
		log.Errorw("unknown IPC status", "status", s)
	}
	return nil
}

func (r *Runner) updateSchema() {
	log := logger.Sugar()
	log.Infow("updating OpenAPI schema")
	p := path.Join(r.workingDir, "openapi.json")
	bs, err := os.ReadFile(p)
	if err != nil {
		log.Errorw("failed to read openapi.json", "path", p, "error", err)
		return
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(bs)
	if err != nil {
		log.Errorw("failed to load OpenAPI schema", "error", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.schema = string(bs)
	r.doc = doc
}

func (r *Runner) updateSetupResult() {
	log := logger.Sugar()
	log.Infow("updating setup result")
	logs := r.rotateLogs()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setupResult.Logs = logs
	if err := r.readJson("setup_result.json", &r.setupResult); err != nil {
		log.Errorw("failed to read setup_result.json", "error", err)
		r.setupResult.Status = SetupFailed
		return
	}
	if r.setupResult.Status == SetupSucceeded {
		log.Infow("setup succeeded")
		r.status = StatusReady
	} else if r.setupResult.Status == SetupFailed {
		log.Errorw("setup failed")
		r.status = StatusSetupFailed
	} else {
		log.Fatalw("invalid setup status", "status", r.setupResult.Status)
	}
}

func (r *Runner) handleResponses() error {
	log := logger.Sugar()
	entries, err := os.ReadDir(r.workingDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}
	for _, entry := range entries {
		// Entries are sorted, so we process response of the same prediction ID in increasing epoch
		m := ResponseRegex.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		pid := m[1]
		r.mu.Lock()
		pr, ok := r.pending[pid]
		if !ok {
			r.mu.Unlock()
			continue
		}
		r.mu.Unlock()

		pr.mu.Lock()
		log.Infow("received prediction response", "id", pid)
		if err := r.readJson(entry.Name(), &pr.response); err != nil {
			log.Errorw("failed to read prediction response", "error", err)
			continue
		}
		// Delete response immediately to avoid duplicates
		if err := os.Remove(path.Join(r.workingDir, entry.Name())); err != nil {
			log.Errorw("failed to delete prediction response", "error", err)
		}

		paths := make([]string, 0)
		outputFn := outputToBase64
		if pr.request.OutputFilePrefix != "" {
			outputFn = outputToUpload(pr.request.OutputFilePrefix, pr.response.Id)
		} else if r.uploadUrl != "" {
			outputFn = outputToUpload(r.uploadUrl, pr.response.Id)
		}
		cachedOutputFn := func(s string, paths *[]string) (string, error) {
			// Cache already handled output files to avoid duplicates or deleted files in Iterator[Path]
			if cache, ok := pr.outputCache[s]; ok {
				return cache, nil
			}
			o, err := outputFn(s, paths)
			if err != nil {
				return "", err
			}
			if o != s {
				// Output path converted to base64 or upload URL, cache it
				pr.outputCache[s] = o
			}
			return o, nil
		}

		if output, err := handlePath(pr.response.Output, &paths, cachedOutputFn); err != nil {
			log.Errorw("failed to handle output path", "id", pid, "error", err)
			pr.response.Status = PredictionFailed
			pr.response.Error = fmt.Sprintf("failed to handle output path: %s", err)
		} else {
			pr.response.Output = output
		}
		// Some models return a hard-coded baked-in file, do not delete them
		// for _, p := range paths {
		// 	if err := os.Remove(p); err != nil {
		// 		log.Errorw("failed to delete output file", "path", p, "error", err)
		// 	}
		// }
		pr.mu.Unlock()

		if pr.response.Status == PredictionStarting {
			log.Infow("prediction started", "id", pr.request.Id, "status", pr.response.Status)
			// Compat: legacy Cog never sends "start" event
			pr.response.Status = PredictionProcessing
			pr.sendWebhook(WebhookStart)
		} else if pr.response.Status == PredictionProcessing {
			log.Infow("prediction processing", "id", pr.request.Id, "status", pr.response.Status)
			pr.sendWebhook(WebhookOutput)
		} else if pr.response.Status.IsCompleted() {
			if pr.response.Status == PredictionSucceeded {
				completedAt, err := util.ParseTime(pr.response.CompletedAt)
				if err != nil {
					return fmt.Errorf("failed to parse time: %w", err)
				}
				startedAt, err := util.ParseTime(pr.response.StartedAt)
				if err != nil {
					return fmt.Errorf("failed to parse time: %w", err)
				}
				t := completedAt.Sub(startedAt).Seconds()
				if pr.response.Metrics == nil {
					pr.response.Metrics = make(map[string]any)
				}
				pr.response.Metrics["predict_time"] = t
			}
			log.Infow("prediction completed", "id", pr.request.Id, "status", pr.response.Status)
			pr.sendWebhook(WebhookCompleted)
			pr.sendResponse()
			for _, p := range pr.inputPaths {
				if err := os.Remove(p); err != nil {
					log.Errorw("failed to delete input file", "path", p, "error", err)
				}
			}
			r.mu.Lock()
			delete(r.pending, pid)
			r.mu.Unlock()
		}
	}
	return nil
}

func (r *Runner) readJson(filename string, v any) error {
	log := logger.Sugar()
	p := path.Join(r.workingDir, filename)
	bs, err := os.ReadFile(p)
	if err != nil {
		log.Errorw("failed to read JSON file", "filename", filename, "error", err)
		return err
	}
	return json.Unmarshal(bs, v)
}

////////////////////
// Log handling

func (r *Runner) log(line string, stderr bool) {
	log := logger.Sugar()
	if m := LogRegex.FindStringSubmatch(line); m != nil {
		pid := m[1]
		msg := m[2]
		r.mu.Lock()
		defer r.mu.Unlock()
		if pr, ok := r.pending[pid]; ok {
			pr.appendLogLine(msg)
			// In case log is received before "starting" response
			if pr.response.Status != "" {
				pr.sendWebhook(WebhookLogs)
			}
		} else {
			log.Errorw("received log for non-existent prediction", "id", pid, "message", msg)
		}
		// Strip [pid=*] prefix before printing
		line = msg
	} else if !strings.Contains(line, "[coglet]") {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.setupResult.CompletedAt != "" && len(r.pending) == 1 && !r.asyncPredict {
			// Anything from inside would be a subprocess call. If it's an async
			// prediction though, we have no clue whose process is whose - this
			// can lead to us leaking outputs from one user to another so we
			// shouldn't keep the lines here
			for pid := range r.pending {
				pr := r.pending[pid]
				pr.appendLogLine(line)
				// In case log is received before "starting" response
				if pr.response.Status != "" {
					pr.sendWebhook(WebhookLogs)
				}
			}
		} else {
			r.logs = append(r.logs, line)
			r.setupResult.Logs = util.JoinLogs(r.logs)
		}
	}
	// Pipe Python stdout/stderr to the corresponding streams
	if stderr {
		fmt.Fprintln(os.Stderr, line)
	} else {
		fmt.Println(line)
	}
}

func (r *Runner) rotateLogs() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs := util.JoinLogs(r.logs)
	r.logs = make([]string, 0)
	return logs
}

func (r *Runner) setupLogging(cmdStart chan bool) error {
	scan := func(f func() (io.ReadCloser, error), stderr bool) error {
		reader, err := f()
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(reader)
		go func() {
			<-cmdStart // Block on command start
			for scanner.Scan() {
				line := scanner.Text()
				r.log(line, stderr)
			}
		}()
		return nil
	}
	if err := scan(r.cmd.StdoutPipe, false); err != nil {
		return err
	}
	if err := scan(r.cmd.StderrPipe, true); err != nil {
		return err
	}
	return nil
}

func mergeEnv(env []string, envSet map[string]string, envUnset []string) []string {
	environment := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		environment[parts[0]] = parts[1]
	}
	for k, v := range envSet {
		environment[k] = v
	}
	for _, k := range envUnset {
		delete(environment, k)
	}
	finalEnv := make([]string, 0, len(environment))
	for k, v := range environment {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}
	return finalEnv
}
