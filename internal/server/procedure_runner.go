package server

import (
	"bufio"
	"context"
	"crypto/md5" // nolint:gosec
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

	ErrMissingProcedureSourceURL = errors.New("missing procedure source url")
	ErrMissingProcedureToken     = errors.New("missing procedure token")
)

type ProcedureRunner struct {
	workingDir   string
	schema       string
	pendingName  string
	procedureKey string
	uploadUrl    string

	logs []string

	setupResult SetupResult
	status      Status

	pending *PendingPrediction
	cmd     *exec.Cmd
	eg      *errgroup.Group

	awaitExplicitShutdown bool
	shutdownRequested     bool
	debug                 bool

	mu sync.Mutex

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

	log.Infow(
		"configuration",
		"working-dir", workingDir,
		"await-explicit-shutdown", cfg.AwaitExplicitShutdown,
		"upload-url", cfg.UploadUrl,
	)

	now := util.NowIso()

	return &ProcedureRunner{
		workingDir:            workingDir,
		status:                StatusStarting,
		awaitExplicitShutdown: cfg.AwaitExplicitShutdown,
		uploadUrl:             cfg.UploadUrl,

		// NOTE: procedures do not have a schema or run a setup per se
		schema: "{}",
		setupResult: SetupResult{
			StartedAt:   now,
			CompletedAt: now,
			Status:      SetupSucceeded,
		},
	}, nil
}

func (r *ProcedureRunner) Schema() string           { return r.schema }
func (r *ProcedureRunner) SetupResult() SetupResult { return r.setupResult }
func (r *ProcedureRunner) Status() Status           { return r.status }

func (r *ProcedureRunner) Start() error {
	go r.handleSignals()

	return nil
}

func (r *ProcedureRunner) setup(proc *ProcedureRequest) error {
	log := logger.Sugar()

	log.Debugw("running setup for procedure", "proc", proc)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ctxCancel != nil {
		r.ctxCancel()
	}

	if r.eg != nil {
		log.Debug("waiting on previous errgroup")

		if err := r.eg.Wait(); err != nil {
			log.Errorw("failed to wait for previous errgroup", "err", err)
		}
	}

	log.Debug("resetting context, cancel func, and errgroup")
	ctx, cancel := context.WithCancel(context.Background())
	r.ctx = ctx
	r.ctxCancel = cancel
	eg, _ := errgroup.WithContext(r.ctx)
	r.eg = eg

	if r.cmd != nil && r.cmd.Process != nil {
		log.Debugw("terminating previous command (best effort)")

		if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Errorw("failed to terminate previous process", "err", err)
		}

		if _, err := r.cmd.Process.Wait(); err != nil {
			log.Errorw("failed to wait for previous process", "err", err)
		}
	}

	r.procedureKey = fmt.Sprintf("%x", md5.Sum([]byte(proc.ProcedureSourceURL)))
	procWorkingDir := filepath.Join(r.workingDir, r.procedureKey)

	if _, err := os.Stat(filepath.Join(procWorkingDir, "cog.yaml")); err != nil {
		log.Debugw("cog.yaml does not exist; copying procedure source", "dest", procWorkingDir)

		if err := os.MkdirAll(procWorkingDir, 0o700); err != nil {
			return err
		}

		if err := os.CopyFS(
			procWorkingDir,
			os.DirFS(strings.TrimPrefix(proc.ProcedureSourceURL, "file://")),
		); err != nil {
			return err
		}
	}

	r.cmd = exec.CommandContext(
		r.ctx,
		"python3",
		"-u",
		"-m", "coglet",
		"--working-dir", procWorkingDir,
	)

	r.cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("PYTHONPATH=%s:%s", procWorkingDir, os.Getenv("PYTHONPATH")),
	)

	if r.debug {
		r.cmd.Env = append(os.Environ(), "LOG_LEVEL=debug")
	}

	cmdStart := make(chan bool)
	if err := r.setupLogging(cmdStart); err != nil {
		log.Errorw("failed to setup logging", "error", err)

		return err
	}

	if err := r.cmd.Start(); err != nil {
		log.Errorw("failed to start command", "error", err, "cmd", r.cmd.Path, "args", r.cmd.Args)
		return err
	}

	log.Infow("command started", "pid", r.cmd.Process.Pid, "cmd", r.cmd.Path, "args", r.cmd.Args)

	close(cmdStart)

	r.eg.Go(func() error {
		return r.config(procWorkingDir)
	})
	r.eg.Go(r.wait)

	return nil
}

func (r *ProcedureRunner) Shutdown() error {
	log := logger.Sugar()

	log.Infow("shutdown requested")

	r.mu.Lock()
	r.shutdownRequested = true
	r.mu.Unlock()

	if r.cmd != nil && r.cmd.ProcessState != nil {
		// Python process already exited
		// Terminate HTTP server
		return r.stop()
	} else {
		// Otherwise signal Python process to stop
		// FIXME: kill process after grace period
		p := filepath.Join(r.workingDir, r.procedureKey, "stop")
		return os.WriteFile(p, []byte{}, 0644)
	}
}

func (r *ProcedureRunner) ExitCode() int {
	if r.cmd == nil || r.cmd.ProcessState == nil {
		return 255
	}

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

	if r.pending != nil {
		r.mu.Unlock()
		log.Errorw("prediction rejected: Already running a prediction")

		return nil, ErrConflict
	}

	if req.Id == r.pendingName {
		r.mu.Unlock()
		log.Errorw("prediction rejected: prediction exists", "id", req.Id)

		return nil, ErrExists
	}

	r.mu.Unlock()

	inputJSONBytes, err := json.Marshal(req.Input)
	if err != nil {
		log.Errorw("failed to re-marshal prediction input", "err", err)

		return nil, err
	}

	proc := &ProcedureRequest{}
	if err := json.Unmarshal(inputJSONBytes, proc); err != nil {
		log.Errorw("input could not be unmarshaled into procedure", "err", err)

		return nil, err
	}

	proc.ProcedureSourceURL = strings.TrimSpace(proc.ProcedureSourceURL)
	proc.Token = strings.TrimSpace(proc.Token)

	if proc.ProcedureSourceURL == "" {
		log.Errorw("missing procedure source url", "err", ErrMissingProcedureSourceURL)

		return nil, ErrMissingProcedureSourceURL
	}

	if proc.Token == "" {
		log.Errorw("missing procedure token", "err", ErrMissingProcedureToken)

		return nil, ErrMissingProcedureToken
	}

	procInputsString := ""
	if err := json.Unmarshal(proc.InputsJSON, &procInputsString); err != nil {
		log.Errorw("nested procedure inputs could not be unmarshaled into string", "err", err)

		return nil, err
	}

	procInputs := map[string]any{}
	if err := json.Unmarshal([]byte(procInputsString), &procInputs); err != nil {
		log.Errorw("nested procedure inputs could not be unmarshaled", "err", err)

		return nil, err
	}

	log.Infow(
		"received procedure request",
		"id", req.Id,
		"procedure_source_url", proc.ProcedureSourceURL,
	)

	curProcedureKey := fmt.Sprintf("%x", md5.Sum([]byte(proc.ProcedureSourceURL)))
	if curProcedureKey != r.procedureKey || r.cmd == nil {
		if err := r.setup(proc); err != nil {
			log.Errorw("failed to setup procedure", "err", err)

			return nil, err
		}
	} else {
		log.Debug("reusing matching procedure environment", "key", r.procedureKey)
	}

	inputPaths := []string{}

	log.Debug("handling base64 input paths")

	input, err := handlePath(procInputs, &inputPaths, base64ToInput)
	if err != nil {
		return nil, err
	}

	log.Debug("handling url input paths")

	input, err = handlePath(procInputs, &inputPaths, urlToInput)
	if err != nil {
		return nil, err
	}

	req.Input = input

	reqPath := filepath.Join(r.workingDir, r.procedureKey, fmt.Sprintf("request-%s.json", req.Id))

	log.Debugw("writing procedure request file", "path", reqPath)

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
		log.Debug("assigning prediction response channel to pending prediction")

		pr.c = make(chan *PredictionResponse, 1)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	log.Debug("assigning pending procedure")
	r.pending = pr

	return pr.c, nil
}

func (r *ProcedureRunner) Cancel(pid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pid != r.pendingName {
		return ErrNotFound
	}

	return syscall.Kill(r.cmd.Process.Pid, syscall.SIGUSR1)
}

////////////////////
// Background tasks

func (r *ProcedureRunner) config(procWorkingDir string) error {
	log := logger.Sugar()

	log.Debug("configuring child runner")

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
		wd, err := os.Getwd()
		if err != nil {
			log.Errorw("failed to get current working directory", "err", err)

			return err
		}

		if err := os.Chdir(procWorkingDir); err != nil {
			log.Errorw("failed to change working directory", "err", err, "dir", procWorkingDir)

			return err
		}

		defer func() {
			if err := os.Chdir(wd); err != nil {
				log.Errorw("failed to change working directory", "err", err, "dir", wd)
			}
		}()

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
	confFile := filepath.Join(procWorkingDir, "config.json")

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

	log.Debug("done configuring child runner")

	return nil
}

func (r *ProcedureRunner) wait() error {
	log := logger.Sugar()

	log.Debugw("waiting on command", "cmd_path", r.cmd.Path, "cmd_args", r.cmd.Args)

	if err := r.cmd.Wait(); err != nil {
		runnerLogs := r.rotateLogs()

		log.Errorw(
			"python runner exited with error",
			"pid", r.cmd.Process.Pid,
			"err", err,
			"logs", runnerLogs,
		)

		r.pending.mu.Lock()

		now := util.NowIso()
		if r.pending.response.StartedAt == "" {
			r.pending.response.StartedAt = now
		}
		r.pending.response.CompletedAt = now
		r.pending.response.Logs += runnerLogs
		r.pending.response.Error = "prediction failed"
		r.pending.response.Status = PredictionFailed

		r.pending.mu.Unlock()

		r.pending.sendWebhook(WebhookCompleted)
		r.pending.sendResponse()
	} else {
		log.Infow("python runner exited successfully", "pid", r.cmd.Process.Pid)
	}

	log.Debug("setting pending and cmd to nil")
	r.pending = nil
	r.cmd = nil

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

func (r *ProcedureRunner) handleResponses() error {
	log := logger.Sugar()

	dirEnts, err := os.ReadDir(filepath.Join(r.workingDir, r.procedureKey))
	if err != nil {
		return fmt.Errorf("failed to read working dir entries %v: %w", filepath.Join(r.workingDir, r.procedureKey), err)
	}

	for _, entry := range dirEnts {
		m := RESPONSE_REGEX.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}

		pid := m[1]

		r.mu.Lock()
		if pid != r.pendingName {
			r.mu.Unlock()
			continue
		}
		r.mu.Unlock()

		log.Infow("received prediction response", "id", pid)
		r.pending.mu.Lock()

		if err := r.readJson(entry.Name(), &r.pending.response); err != nil {
			r.pending.mu.Unlock()
			return fmt.Errorf("failed to read output json from %v: %w", entry.Name(), err)
		}

		// Delete response immediately to avoid duplicates
		if err := os.Remove(filepath.Join(r.workingDir, r.procedureKey, entry.Name())); err != nil {
			r.pending.mu.Unlock()
			return fmt.Errorf("failed to remove output %v: %w", entry.Name(), err)
		}

		paths := []string{}
		outputFn := outputToBase64

		if r.pending.request.OutputFilePrefix != "" {
			outputFn = outputToUpload(r.pending.request.OutputFilePrefix, r.pending.response.Id)
		} else if r.uploadUrl != "" {
			outputFn = outputToUpload(r.uploadUrl, r.pending.response.Id)
		}

		if output, err := handlePath(r.pending.response.Output, &paths, outputFn); err != nil {
			log.Errorw("failed to handle output", "id", pid, "error", err)
			r.pending.response.Status = PredictionFailed
			r.pending.response.Error = err.Error()
		} else {
			r.pending.response.Output = output
		}

		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				log.Errorw("failed to remove path", "path", p, "err", err)
			}
		}

		r.pending.mu.Unlock()

		if r.pending.response.Status == PredictionStarting {
			log.Infow("prediction started", "id", r.pending.request.Id, "status", r.pending.response.Status)

			// NOTE: for compatibility since legacy Cog never sends "start" event
			r.pending.response.Status = PredictionProcessing
			r.pending.sendWebhook(WebhookStart)
		} else if r.pending.response.Status == PredictionProcessing {
			log.Infow("prediction processing", "id", r.pending.request.Id, "status", r.pending.response.Status)

			r.pending.sendWebhook(WebhookOutput)
		} else if r.pending.response.Status.IsCompleted() {
			if r.pending.response.Status == PredictionSucceeded {
				predictTimeSeconds := util.ParseTime(r.pending.response.CompletedAt).Sub(util.ParseTime(r.pending.response.StartedAt)).Seconds()

				if r.pending.response.Metrics == nil {
					r.pending.response.Metrics = map[string]any{}
				}

				r.pending.response.Metrics["predict_time"] = predictTimeSeconds
			}

			log.Infow("prediction completed", "id", r.pending.request.Id, "status", r.pending.response.Status)

			if err := r.pending.sendWebhook(WebhookCompleted); err != nil {
				return err
			}

			r.pending.sendResponse()

			for _, p := range r.pending.inputPaths {
				if err := os.Remove(p); err != nil {
					log.Errorw("failed to remove path", "path", p, "err", err)
				}
			}

			r.mu.Lock()
			r.pendingName = ""
			r.pending = nil
			r.mu.Unlock()
		}
	}

	return nil
}

func (r *ProcedureRunner) readJson(filename string, v any) error {
	log := logger.Sugar()
	p := filepath.Join(r.workingDir, r.procedureKey, filename)
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

		if pid == r.pendingName {
			r.pending.appendLogLine(msg)
			// In case log is received before "starting" response
			if r.pending.response.Status != "" {
				if err := r.pending.sendWebhook(WebhookLogs); err != nil {
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
