package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/replicate/cog-runtime/internal/util"
)

var (
	LogRegex      = regexp.MustCompile(`^\[pid=(?P<pid>[^]]+)] (?P<msg>.*)$`)
	ResponseRegex = regexp.MustCompile(`^response-(?P<pid>\S+)-(?P<epoch>\d+).json$`)
	CancelFmt     = "cancel-%s"
)

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
		log.Errorw("failed to send webhook", "url", pr.request.Webhook, "error", err)
	}
}

func (pr *PendingPrediction) sendResponse() {
	if pr.c == nil {
		return
	}
	pr.c <- pr.response
}

// killFunc is the function signature for killing processes
type killFunc func(pid int, sig syscall.Signal) error

// verifyProcessGroupTerminatedFunc is the function signature for verifying process group termination
type verifyProcessGroupTerminatedFunc func(pid int) bool

// verifyProcessGroupTerminated checks if all processes in the group have been terminated
// Returns true if all processes are gone, false if any are still running
func verifyProcessGroupTerminated(pid int) bool {
	log := logger.Sugar()

	// Try to send signal 0 to the process group to check if any processes still exist
	// Signal 0 doesn't actually send a signal but checks if the process exists
	err := syscall.Kill(-pid, 0)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			// No such process - process group is terminated
			log.Debugw("process group fully terminated", "pid", pid)
			return true
		}
		// Other errors (like EPERM) mean processes might still exist
		log.Debugw("process group verification failed, assuming processes exist", "pid", pid, "error", err)
		return false
	}
	// No error means at least one process in the group still exists
	log.Debugw("process group still has running processes", "pid", pid)
	return false
}

type Runner struct {
	name                string
	workingDir          string
	tmpDir              string // temp directory for process isolation
	cmd                 exec.Cmd
	status              Status
	schema              string
	doc                 *openapi3.T
	setupResult         SetupResult
	logs                []string
	asyncPredict        bool
	maxConcurrency      int
	pending             map[string]*PendingPrediction
	uploadURL           string
	shutdownGracePeriod time.Duration
	cleanupTimeout      time.Duration                    // timeout for process cleanup verification
	killed              bool                             // tracks if we've killed this process instance
	cleanupSlot         chan struct{}                    // buffered size 1, holds cleanup token (len()=1 means no cleanup, len()=0 means cleanup in progress)
	forceShutdown       chan<- struct{}                  // signals that cleanup failed and forced shutdown is needed
	killFn              killFunc                         // injectable kill function for testing
	verifyFn            verifyProcessGroupTerminatedFunc // injectable verification function for testing
	mu                  sync.Mutex
	stopped             chan bool
}

const (
	DefaultRunnerID   = 0
	DefaultRunnerName = "default"
)

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
	// Use CommandContext so we can cancel the process tree
	ctx := context.Background()                             // We do not have a clear context through the whole stack yet, so mint a context here.
	cmd := exec.CommandContext(ctx, pythonBinPath, args...) //nolint:gosec // expected subprocess launched with variable
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = mergeEnv(os.Environ(), cfg.EnvSet, cfg.EnvUnset)

	r := &Runner{
		name:                name,
		workingDir:          workingDir,
		cmd:                 *cmd,
		status:              StatusStarting,
		maxConcurrency:      1,
		pending:             make(map[string]*PendingPrediction),
		uploadURL:           cfg.UploadURL,
		shutdownGracePeriod: cfg.RunnerShutdownGracePeriod,
		cleanupTimeout:      cfg.CleanupTimeout,
		killFn:              nil, // nil means use real syscall.Kill
		verifyFn:            nil, // nil means use real verifyProcessGroupTerminated
		cleanupSlot:         make(chan struct{}, 1),
		forceShutdown:       cfg.ForceShutdown,
		stopped:             make(chan bool),
	}

	// Initialize cleanup slot with token available (no cleanup in progress)
	r.cleanupSlot <- struct{}{}

	return r, nil
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

func (r *Runner) Start(ctx context.Context) error {
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
	return nil
}

// ForceKill immediately kills the runner process group
func (r *Runner) ForceKill() {
	r.mu.Lock()
	defer r.mu.Unlock()

	log := logger.Sugar()

	// Skip if already killed, no process, or process already exited
	if r.killed || r.cmd.Process == nil || r.cmd.ProcessState != nil {
		if r.cmd.ProcessState != nil {
			log.Infow("process already exited, nothing to do")
		}
		return
	}

	log.Infow("force killing process group", "pid", r.cmd.Process.Pid)

	// Try to take cleanup token
	gotToken := false
	select {
	case <-r.cleanupSlot:
		gotToken = true
		log.Infow("acquired cleanup token", "pid", r.cmd.Process.Pid)
	default:
		log.Infow("cleanup already in progress, but proceeding with kill", "pid", r.cmd.Process.Pid)
	}

	// Use injected kill function for testing, or real syscall.Kill
	killFn := r.killFn
	if killFn == nil {
		killFn = syscall.Kill
	}

	if err := killFn(-r.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// ESRCH means process already dead - that's expected and fine
		if !errors.Is(err, syscall.ESRCH) {
			log.Errorw("failed to kill process group", "pid", r.cmd.Process.Pid, "error", err)
		}
		// Return token only if we took it and kill failed (unless ESRCH which is OK)
		if gotToken && !errors.Is(err, syscall.ESRCH) {
			r.cleanupSlot <- struct{}{}
			return
		}
	}

	// Mark as killed to prevent PID reuse issues
	r.killed = true

	// Start verification of process termination in background only if we got the token
	if gotToken {
		go r.verifyProcessCleanup(r.cmd.Process.Pid)
	}
}

// verifyProcessCleanup verifies that all processes in the group have been terminated
// and updates the cleanup status accordingly
func (r *Runner) verifyProcessCleanup(pid int) {
	log := logger.Sugar()
	const checkInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), r.cleanupTimeout)
	defer cancel()

	start := time.Now()
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopped:
			// Runner is being stopped, exit cleanup verification
			log.Debugw("cleanup verification stopped due to runner shutdown", "pid", pid, "elapsed", time.Since(start))
			return
		case <-ctx.Done():
			// Timeout reached - signal forced shutdown
			log.Errorw("process cleanup verification timed out, signaling forced shutdown",
				"pid", pid, "elapsed", time.Since(start))
			select {
			case r.forceShutdown <- struct{}{}:
			default:
			}
			return
		case <-ticker.C:
			fn := verifyProcessGroupTerminated
			if r.verifyFn != nil {
				fn = r.verifyFn
			}
			if fn(pid) {
				// Cleanup completed successfully, return token
				r.cleanupSlot <- struct{}{}
				log.Infow("process cleanup completed successfully, returned cleanup token", "pid", pid, "elapsed", time.Since(start))
				return
			}
		}
	}
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
		return nil
	}

	// Signal graceful shutdown
	p := filepath.Join(r.workingDir, "stop")
	if err := os.WriteFile(p, []byte{}, 0o644); err != nil { //nolint:gosec // TODO: evaluate if 0o644 is correct mode
		log.Errorw("failed to write stop file", "error", err)
	}

	// Start grace period timer if configured
	if r.shutdownGracePeriod > 0 {
		go func() {
			timer := time.NewTimer(r.shutdownGracePeriod)
			defer timer.Stop()

			select {
			case <-timer.C:
				// Grace period expired, force kill
				log.Infow("grace period expired, force killing", "gracePeriod", r.shutdownGracePeriod)
				r.ForceKill()
			case <-r.stopped:
				// Process exited gracefully, timer will be cleaned up by defer
				return
			}
		}()
	} else {
		// No grace period, force kill immediately
		r.ForceKill()
	}

	return nil
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
	switch r.status {
	case StatusSetupFailed:
		log.Errorw("prediction rejected: setup failed")
		return nil, ErrSetupFailed
	case StatusDefunct:
		log.Errorw("prediction rejected: server is defunct")
		return nil, ErrDefunct
	}
	r.mu.Lock()
	if len(r.pending) >= r.maxConcurrency {
		r.mu.Unlock()
		log.Errorw("prediction rejected: Already running a prediction")
		return nil, ErrConflict
	}
	if _, ok := r.pending[req.ID]; ok {
		r.mu.Unlock()
		log.Errorw("prediction rejected: prediction exists", "id", req.ID)
		return nil, ErrExists
	}
	r.mu.Unlock()

	log.Infow("received prediction request", "id", req.ID)
	if req.CreatedAt == "" {
		req.CreatedAt = util.NowIso()
	}
	// Start here so that input downloads are counted towards predict_time
	if req.StartedAt == "" {
		req.StartedAt = util.NowIso()
	}

	inputPaths := make([]string, 0)
	input, err := processInputPaths(req.Input, r.doc, &inputPaths, base64ToInput)
	if err != nil {
		return nil, err
	}
	input, err = processInputPaths(input, r.doc, &inputPaths, urlToInput)
	if err != nil {
		return nil, err
	}
	req.Input = input

	reqPath := path.Join(r.workingDir, fmt.Sprintf("request-%s.json", req.ID))
	bs, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	if err := os.WriteFile(reqPath, bs, 0o644); err != nil { //nolint:gosec // TODO: evaluate if 0o644 is correct mode
		return nil, err
	}
	resp := PredictionResponse{
		Input:     req.Input,
		ID:        req.ID,
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
	r.pending[req.ID] = &pr
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
		return os.WriteFile(p, []byte{}, 0o644) //nolint:gosec // TODO: evaluate if 0o644 is correct mode
	}
	// Blocking predict, use SIGUSR1 to cancel
	// FIXME: ensure only one prediction in flight?
	return syscall.Kill(r.cmd.Process.Pid, syscall.SIGUSR1)
}

////////////////////
// Background tasks

func (r *Runner) config(ctx context.Context) error {
	log := logger.Sugar()

	// Wait until user files become available and pass config to Python runner
	waitFile := os.Getenv("COG_WAIT_FILE")
	if waitFile != "" {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(waitFile); err == nil {
				// wait file found, break out of loop
				break
			}
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				// context canceled, introspect the error and return
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					log.Errorw("wait file not found after timeout", "wait_file", waitFile)
					return fmt.Errorf("%w: wait file not found after timeout", context.DeadlineExceeded)
				}
				return ctx.Err()
			}
		}
	}

	var moduleName, predictorName string
	y, err := util.ReadCogYaml(r.SrcDir())
	if err != nil {
		log.Errorw("failed to read cog.yaml", "path", r.SrcDir(), "error", err)
		return err
	}
	m, c, err := y.PredictModuleAndPredictor()
	if err != nil {
		log.Errorw("failed to parse predict", "error", err)
		return err
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

	conf := PredictConfig{
		ModuleName:     moduleName,
		PredictorName:  predictorName,
		MaxConcurrency: r.maxConcurrency,
	}
	log.Infow("configuring runner", "module", moduleName, "predictor", predictorName, "max_concurrency", r.maxConcurrency)
	confFile := path.Join(r.workingDir, "config.json")
	f, err := os.Create(confFile) //nolint:gosec // expected dynamic path
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
	bs, err := os.ReadFile(p) //nolint:gosec // expected dynamic path
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
	if err := r.readJSON("setup_result.json", &r.setupResult); err != nil {
		log.Errorw("failed to read setup_result.json", "error", err)
		r.setupResult.Status = SetupFailed
		return
	}
	switch r.setupResult.Status {
	case SetupSucceeded:
		log.Infow("setup succeeded")
		r.status = StatusReady
	case SetupFailed:
		log.Errorw("setup failed")
		r.status = StatusSetupFailed
	default:
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
		if err := r.readJSON(entry.Name(), &pr.response); err != nil {
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
			outputFn = outputToUpload(pr.request.OutputFilePrefix, pr.response.ID)
		} else if r.uploadURL != "" {
			outputFn = outputToUpload(r.uploadURL, pr.response.ID)
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

		switch {
		case pr.response.Status == PredictionStarting:
			log.Infow("prediction started", "id", pr.request.ID, "status", pr.response.Status)
			// Compat: legacy Cog never sends "start" event
			pr.response.Status = PredictionProcessing
			pr.sendWebhook(WebhookStart)
		case pr.response.Status == PredictionProcessing:
			log.Infow("prediction processing", "id", pr.request.ID, "status", pr.response.Status)
			pr.sendWebhook(WebhookOutput)
		case pr.response.Status.IsCompleted():
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
			log.Infow("prediction completed", "id", pr.request.ID, "status", pr.response.Status)
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

func (r *Runner) readJSON(filename string, v any) error {
	log := logger.Sugar()
	p := path.Join(r.workingDir, filename)
	bs, err := os.ReadFile(p) //nolint:gosec // expected dynamic path
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
		fmt.Fprintln(os.Stderr, line) //nolint:forbidigo // expected see above comment
	} else {
		fmt.Println(line) //nolint:forbidigo // expected see above comment
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
