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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/replicate/cog-runtime/internal/util"
)

var (
	errInvalidSetupStatus = errors.New("invalid setup status")
)

type ProcedureRunner struct {
	workingDir            string
	cmd                   *exec.Cmd
	status                Status
	schema                string
	setupResult           SetupResult
	logs                  []string
	pending               map[string]*PendingPrediction
	awaitExplicitShutdown bool
	uploadUrl             string
	shutdownRequested     bool
	mu                    sync.Mutex

	ctx       context.Context
	ctxCancel context.CancelFunc
}

func NewProcedureRunner(cfg *Config) (Runner, error) {
	log := logger.Sugar()

	workingDir := cfg.WorkingDir
	if workingDir == "" {
		if v, err := os.MkdirTemp("", "cog-server-"); err != nil {
			return nil, err
		} else {
			workingDir = v
		}
	}

	log.Infow("configuration",
		"working-dir", workingDir,
		"await-explicit-shutdown", cfg.AwaitExplicitShutdown,
		"upload-url", cfg.UploadUrl,
	)

	return &ProcedureRunner{
		workingDir:            workingDir,
		status:                StatusStarting,
		pending:               map[string]*PendingPrediction{},
		awaitExplicitShutdown: cfg.AwaitExplicitShutdown,
		uploadUrl:             cfg.UploadUrl,
	}, nil
}

func (r *ProcedureRunner) Schema() string           { return r.schema }
func (r *ProcedureRunner) SetupResult() SetupResult { return r.setupResult }
func (r *ProcedureRunner) Status() Status           { return r.status }

func (r *ProcedureRunner) Start() error {
	if err := r.setup(); err != nil {
		return err
	}

	go r.handleSignals()

	return nil
}

func (r *ProcedureRunner) setup() error {
	log := logger.Sugar()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ctxCancel != nil {
		r.ctxCancel()
	}

	log.Debugw("resetting context and cancel func")
	ctx, cancel := context.WithCancel(context.Background())
	r.ctx = ctx
	r.ctxCancel = cancel

	if r.cmd != nil && r.cmd.Process != nil {
		log.Debugw("terminating previous command (best effort)")

		if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Errorw("failed to terminate previous process", "err", err)
		}

		if _, err := r.cmd.Process.Wait(); err != nil {
			log.Errorw("failed to wait for previous process", "err", err)
		}
	}

	r.cmd = exec.CommandContext(
		r.ctx,
		"python3",
		"-u",
		"-m", "coglet",
		"--working-dir", r.workingDir,
		"--procedure-mode",
	)

	cmdStart := make(chan bool)
	if err := r.setupLogging(cmdStart); err != nil {
		log.Errorw("failed to setup logging", "error", err)
		return err
	}

	// Placeholder in case setup crashes
	r.setupResult = SetupResult{StartedAt: util.NowIso()}

	if err := r.cmd.Start(); err != nil {
		log.Errorw("failed to start command", "error", err, "cmd", r.cmd.Path, "args", r.cmd.Args)
		return err
	}

	log.Infow("command started", "pid", r.cmd.Process.Pid, "cmd", r.cmd.Path, "args", r.cmd.Args)

	close(cmdStart)

	eg, _ := errgroup.WithContext(r.ctx)

	eg.Go(r.config)
	eg.Go(r.wait)

	return eg.Wait()
}

func (r *ProcedureRunner) Shutdown() error {
	log := logger.Sugar()

	log.Infow("shutdown requested")

	r.mu.Lock()
	r.shutdownRequested = true
	r.mu.Unlock()

	if r.cmd.ProcessState != nil {
		// Python process already exited
		// Terminate HTTP server
		return r.stop()
	} else {
		// Otherwise signal Python process to stop
		// FIXME: kill process after grace period
		p := filepath.Join(r.workingDir, "stop")
		return os.WriteFile(p, []byte{}, 0644)
	}
}

func (r *ProcedureRunner) ExitCode() int {
	return r.cmd.ProcessState.ExitCode()
}

func (r *ProcedureRunner) stop() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}

////////////////////
// Prediction

func (r *ProcedureRunner) Predict(req *PredictionRequest) (chan *PredictionResponse, error) {
	log := logger.Sugar()

	if r.status == StatusSetupFailed {
		log.Errorw("prediction rejected: setup failed")

		return nil, ErrSetupFailed
	} else if r.status == StatusDefunct {
		log.Errorw("prediction rejected: server is defunct")

		return nil, ErrDefunct
	}

	if req.CreatedAt == "" {
		req.CreatedAt = util.NowIso()
	}

	r.mu.Lock()

	if len(r.pending) >= 1 {
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

	inputPaths := []string{}

	input, err := handlePath(req.Input, &inputPaths, base64ToInput)
	if err != nil {
		return nil, err
	}

	input, err = handlePath(req.Input, &inputPaths, urlToInput)
	if err != nil {
		return nil, err
	}

	req.Input = input

	reqPath := filepath.Join(r.workingDir, fmt.Sprintf("request-%s.json", req.Id))

	jsonBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(reqPath, jsonBytes, 0o644); err != nil {
		return nil, err
	}

	resp := &PredictionResponse{
		Input:     req.Input,
		Id:        req.Id,
		CreatedAt: req.CreatedAt,
	}
	pr := &PendingPrediction{
		request:    *req,
		response:   *resp,
		inputPaths: inputPaths,
	}

	if req.Webhook == "" {
		pr.c = make(chan *PredictionResponse, 1)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[req.Id] = pr

	return pr.c, nil
}

func (r *ProcedureRunner) Cancel(pid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.pending[pid]; !ok {
		return ErrNotFound
	}

	return syscall.Kill(r.cmd.Process.Pid, syscall.SIGUSR1)
}

////////////////////
// Background tasks

func (r *ProcedureRunner) config() error {
	log := logger.Sugar()

	if waitFile, ok := os.LookupEnv("COG_WAIT_FILE"); ok && waitFile != "" {
		log.Debugw("waiting until user files become available and then passing config to python runner")

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
				"wait file not found after timeout",
				"elapsed", elapsed,
				"wait_file", waitFile,
			)

			return fmt.Errorf("wait file not found after timeout %s: %s", elapsed, waitFile)
		}
	}

	// NOTE: looking up these values from the environment is for testing only, set by
	// CogTest, to avoid creating a one-off cog.yaml
	moduleName := os.Getenv("COG_MODULE_NAME")
	predictorName := os.Getenv("COG_PREDICTOR_NAME")

	if moduleName == "" || predictorName == "" {
		y, err := util.ReadCogYaml()
		if err != nil {
			log.Errorw("failed to read cog.yaml", "err", err)

			return err
		}

		m, c, err := y.PredictModuleAndPredictor()
		if err != nil {
			log.Errorw("failed to parse predict", "err", err)

			return err
		}

		moduleName = m
		predictorName = c
	}

	conf := PredictionConfig{ModuleName: moduleName, PredictorName: predictorName}
	confFile := filepath.Join(r.workingDir, "config.json")

	f, err := os.Create(confFile)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", confFile, err)
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Errorw("failed to close config file", "err", err)
		}
	}()

	if err := json.NewEncoder(f).Encode(conf); err != nil {
		return fmt.Errorf("failed to encode config to json: %w", err)
	}

	return nil
}

func (r *ProcedureRunner) wait() error {
	log := logger.Sugar()

	if err := r.cmd.Wait(); err != nil {
		runnerLogs := r.rotateLogs()

		log.Errorw(
			"python runner exited with error",
			"pid", r.cmd.Process.Pid,
			"err", err,
			"logs", runnerLogs,
		)

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

	if !r.awaitExplicitShutdown || r.shutdownRequested {
		return r.stop()
	}

	return nil
}

func (r *ProcedureRunner) handleSignals() {
	log := logger.Sugar()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, SigOutput, SigReady, SigBusy)

	for {
		s := <-ch

		if s == SigOutput {
			if err := r.handleResponses(); err != nil {
				log.Errorw("failed to handle responses", "err", err)
			}
		} else if s == SigReady {
			if r.status == StatusStarting {
				if err := r.updateSchema(); err != nil {
					log.Errorw("failed to update schema", "err", err)
				}

				if err := r.updateSetupResult(); err != nil {
					log.Errorw("failed to update setup result", "err", err)
				}

				if err := r.handleReadinessProbe(); err != nil {
					log.Errorw("fail to write ready file", "err", err)
				}
			}

			log.Info("runner is ready")

			r.mu.Lock()
			r.status = StatusReady
			r.mu.Unlock()
		} else if s == SigBusy {
			log.Info("runner is busy")

			r.mu.Lock()
			r.status = StatusBusy
			r.mu.Unlock()
		}
	}
}

func (r *ProcedureRunner) handleReadinessProbe() error {
	// NOTE: this is a compatibility signal for K8S pod readiness probe
	// https://github.com/replicate/cog/blob/main/python/cog/server/probes.py
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		return nil
	}

	dir := "/var/run/cog"

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600); err != nil {
		return err
	}

	return nil
}

////////////////////
// IO handling

func (r *ProcedureRunner) updateSchema() error {
	log := logger.Sugar()

	log.Infow("updating OpenAPI schema")

	if schemaBytes, err := os.ReadFile(filepath.Join(r.workingDir, "openapi.json")); err != nil {
		return err
	} else {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.schema = string(schemaBytes)
	}

	return nil
}

func (r *ProcedureRunner) updateSetupResult() error {
	log := logger.Sugar()

	log.Infow("updating setup result")

	logs := r.rotateLogs()

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.readJson("setup_result.json", &r.setupResult); err != nil {
		return err
	}

	r.setupResult.Logs = logs

	if r.setupResult.Status == SetupSucceeded {
		log.Infow("setup succeeded")
		r.status = StatusReady
	} else if r.setupResult.Status == SetupFailed {
		log.Errorw("setup failed")
		r.status = StatusSetupFailed
	} else {
		return fmt.Errorf("status=%v: %w", r.setupResult.Status, errInvalidSetupStatus)
	}

	return nil
}

func (r *ProcedureRunner) handleResponses() error {
	log := logger.Sugar()

	dirEnts, err := os.ReadDir(r.workingDir)
	if err != nil {
		return fmt.Errorf("failed to read working dir entries %v: %w", r.workingDir, err)
	}

	for _, entry := range dirEnts {
		m := RESPONSE_REGEX.FindStringSubmatch(entry.Name())
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

		log.Infow("received prediction response", "id", pid)
		pr.mu.Lock()

		if err := r.readJson(entry.Name(), &pr.response); err != nil {
			pr.mu.Unlock()
			return fmt.Errorf("failed to read output json from %v: %w", entry.Name(), err)
		}

		// Delete response immediately to avoid duplicates
		if err := os.Remove(filepath.Join(r.workingDir, entry.Name())); err != nil {
			pr.mu.Unlock()
			return fmt.Errorf("failed to remove output %v: %w", entry.Name(), err)
		}

		paths := []string{}
		outputFn := outputToBase64

		if pr.request.OutputFilePrefix != "" {
			outputFn = outputToUpload(pr.request.OutputFilePrefix, pr.response.Id)
		} else if r.uploadUrl != "" {
			outputFn = outputToUpload(r.uploadUrl, pr.response.Id)
		}

		if output, err := handlePath(pr.response.Output, &paths, outputFn); err != nil {
			log.Errorw("failed to handle output", "id", pid, "error", err)
			pr.response.Status = PredictionFailed
			pr.response.Error = err.Error()
		} else {
			pr.response.Output = output
		}

		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				log.Errorw("failed to remove path", "path", p, "err", err)
			}
		}

		pr.mu.Unlock()

		if pr.response.Status == PredictionStarting {
			log.Infow("prediction started", "id", pr.request.Id, "status", pr.response.Status)

			// NOTE: for compatibility since legacy Cog never sends "start" event
			pr.response.Status = PredictionProcessing
			pr.sendWebhook(WebhookStart)
		} else if pr.response.Status == PredictionProcessing {
			log.Infow("prediction processing", "id", pr.request.Id, "status", pr.response.Status)

			pr.sendWebhook(WebhookOutput)
		} else if pr.response.Status.IsCompleted() {
			if pr.response.Status == PredictionSucceeded {
				predictTimeSeconds := util.ParseTime(pr.response.CompletedAt).Sub(util.ParseTime(pr.response.StartedAt)).Seconds()

				if pr.response.Metrics == nil {
					pr.response.Metrics = map[string]any{}
				}

				pr.response.Metrics["predict_time"] = predictTimeSeconds
			}

			log.Infow("prediction completed", "id", pr.request.Id, "status", pr.response.Status)

			if err := pr.sendWebhook(WebhookCompleted); err != nil {
				return err
			}

			pr.sendResponse()

			for _, p := range pr.inputPaths {
				if err := os.Remove(p); err != nil {
					log.Errorw("failed to remove path", "path", p, "err", err)
				}
			}

			r.mu.Lock()
			delete(r.pending, pid)
			r.mu.Unlock()
		}
	}

	return nil
}

func (r *ProcedureRunner) readJson(filename string, v any) error {
	log := logger.Sugar()
	p := filepath.Join(r.workingDir, filename)
	bs, err := os.ReadFile(p)
	if err != nil {
		log.Errorw("failed to read JSON file", "filename", filename, "error", err)
		return err
	}
	return json.Unmarshal(bs, v)
}

////////////////////
// Log handling

func (r *ProcedureRunner) log(line string) {
	log := logger.Sugar()

	if m := LOG_REGEX.FindStringSubmatch(line); m != nil {
		pid := m[1]
		msg := m[2]

		r.mu.Lock()
		defer r.mu.Unlock()

		if pr, ok := r.pending[pid]; ok {
			pr.appendLogLine(msg)
			// In case log is received before "starting" response
			if pr.response.Status != "" {
				if err := pr.sendWebhook(WebhookLogs); err != nil {
					log.Errorw("failed to send early log webhook", "err", err)
				}
			}
		} else {
			log.Errorw("received log for non-existent prediction", "id", pid, "message", msg)
		}
	} else if !strings.Contains(line, "[coglet]") {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.logs = append(r.logs, line)
		r.setupResult.Logs = util.JoinLogs(r.logs)
	}

	fmt.Println(line)
}

func (r *ProcedureRunner) rotateLogs() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	logs := util.JoinLogs(r.logs)
	r.logs = make([]string, 0)

	return logs
}

func (r *ProcedureRunner) setupLogging(cmdStart chan bool) error {
	scan := func(f func() (io.ReadCloser, error)) error {
		reader, err := f()
		if err != nil {
			return err
		}

		scanner := bufio.NewScanner(reader)

		go func() {
			select {
			case <-r.ctx.Done():
				return
			case <-cmdStart:
				// block on command start
			}

			for scanner.Scan() {
				line := scanner.Text()
				r.log(line)
			}
		}()

		return nil
	}

	if err := scan(r.cmd.StdoutPipe); err != nil {
		return err
	}

	if err := scan(r.cmd.StderrPipe); err != nil {
		return err
	}

	return nil
}
