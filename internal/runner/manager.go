package runner

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/replicate/cog-runtime/internal/checkpointer"
	"github.com/replicate/cog-runtime/internal/config"
	"github.com/replicate/cog-runtime/internal/logging"
	"github.com/replicate/cog-runtime/internal/webhook"
)

//go:embed openapi-procedure.json
var procedureSchema string

var (
	ErrNoCapacity          = errors.New("no runner capacity available")
	ErrPredictionNotFound  = errors.New("prediction not found")
	ErrRunnerNotFound      = errors.New("runner not found")
	ErrNoEmptySlot         = errors.New("no empty slot available")
	ErrInvalidRunnerStatus = errors.New("invalid runner status for new prediction")
	// ErrAsyncPrediction is a sentinel error used to indicate that a prediction is being served asynchronously, it is not surfaced outside of runner
	ErrAsyncPrediction = errors.New("async prediction")
)

// Manager manages the lifecycle and capacity of prediction runners
type Manager struct {
	ctx           context.Context //nolint:containedctx // this is a root context derived from the server context that all runners will derive their ctx from
	cfg           config.Config
	runners       []*Runner
	capacity      chan struct{}
	stopped       chan struct{}
	stopOnce      sync.Once
	webhookSender webhook.Sender
	monitoringWG  sync.WaitGroup // tracks monitoring goroutines for clean shutdown

	mu sync.RWMutex

	baseLogger *logging.Logger // base logger passed from parent, used to create named loggers for runners
	logger     *logging.Logger
}

// NewManager creates a new runner manager with channel-based capacity control
func NewManager(ctx context.Context, cfg config.Config, logger *logging.Logger) *Manager {
	m := newManager(ctx, cfg, logger)
	// Pre-load default runner in non-procedure mode
	if !cfg.UseProcedureMode {
		_, err := m.createDefaultRunner(ctx)
		if err != nil {
			m.logger.Error("failed to create default runner", zap.Error(err))
		}
	}
	return m
}

func newManager(ctx context.Context, cfg config.Config, logger *logging.Logger) *Manager {
	maxRunners := cfg.MaxRunners
	if cfg.UseProcedureMode {
		if cfg.OneShot {
			maxRunners = 1
		} else if maxRunners == 0 {
			maxRunners = runtime.NumCPU() * 4
		}
	} else {
		// For non-procedure mode, read cog.yaml to determine capacity
		workingDir := cfg.WorkingDirectory
		if workingDir == "" {
			var err error
			workingDir, err = os.Getwd()
			if err != nil {
				logger.Warn("failed to get working directory for cog.yaml reading", zap.Error(err))
				maxRunners = 1
			} else {
				cogYaml, err := ReadCogYaml(workingDir)
				if err != nil {
					logger.Warn("failed to read cog.yaml, using default concurrency", zap.Error(err))
					maxRunners = 1
				} else {
					maxRunners = max(1, cogYaml.Concurrency.Max)
					logger.Trace("read concurrency from cog.yaml", zap.Int("max_concurrency", maxRunners))
				}
			}
		} else {
			cogYaml, err := ReadCogYaml(workingDir)
			if err != nil {
				logger.Warn("failed to read cog.yaml, using default concurrency", zap.Error(err))
				maxRunners = 1
			} else {
				maxRunners = max(1, cogYaml.Concurrency.Max)
				logger.Debug("read concurrency from cog.yaml", zap.Int("max_concurrency", maxRunners))
			}
		}
	}

	capacity := make(chan struct{}, maxRunners)
	for i := 0; i < maxRunners; i++ {
		capacity <- struct{}{}
	}

	baseLogger := logger.Named("runner")

	// Create webhook sender
	webhookSender := webhook.NewSender(baseLogger)

	return &Manager{
		ctx:           ctx,
		cfg:           cfg,
		runners:       make([]*Runner, maxRunners),
		capacity:      capacity,
		stopped:       make(chan struct{}),
		webhookSender: webhookSender,
		baseLogger:    baseLogger,
		logger:        baseLogger.Named("manager"),
	}
}

// Start initializes the manager
func (m *Manager) Start(ctx context.Context) error {
	log := m.logger.Sugar()
	log.Info("starting runner manager")

	// In non-procedure mode, the default runner is created and started on-demand
	// No need to start it here since createDefaultRunner() handles that

	return nil
}

func (m *Manager) claimSlot() error {
	select {
	case <-m.capacity:
		m.logger.Trace("claiming slot")
		return nil
	default:
		m.logger.Trace("attempted claim slot but no slot available")
		return ErrNoCapacity
	}
}

func (m *Manager) releaseSlot() {
	select {
	case m.capacity <- struct{}{}:
		m.logger.Trace("releasing slot")
	default:
		m.logger.Error("attempted to release slot but channel is full")
	}
}

// PredictSync executes a sync prediction request - blocks until complete
func (m *Manager) PredictSync(req PredictionRequest) (*PredictionResponse, error) {
	respChan, _, err := m.predict(m.ctx, req, false)
	if err != nil {
		return nil, err
	}

	// Wait for completion and return result
	resp := <-respChan
	return &resp, nil
}

// PredictAsync executes an async prediction request - returns immediately, sends webhook when complete
func (m *Manager) PredictAsync(ctx context.Context, req PredictionRequest) (*PredictionResponse, error) {
	log := m.logger.Sugar()
	respChan, initialResponse, err := m.predict(ctx, req, true)
	if err != nil {
		return nil, err
	}

	// Release slot when prediction completes in background
	go func() {
		<-respChan // Wait for prediction to complete
		log.Tracew("async prediction completed", "prediction_id", req.ID)
	}()

	return initialResponse, nil
}

// predict is the internal implementation shared by both sync and async predictions
func (m *Manager) predict(ctx context.Context, req PredictionRequest, async bool) (chan PredictionResponse, *PredictionResponse, error) {
	log := m.logger.Sugar()
	if err := m.claimSlot(); err != nil {
		return nil, nil, err
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 10*time.Second)
	defer deadlineCancel()

	runner, err := m.assignReqToRunner(deadlineCtx, req, async)
	if err != nil {
		m.releaseSlot()
		return nil, nil, err
	}

	if !m.cfg.UseProcedureMode && runner.status != StatusReady {
		m.releaseSlot()
		return nil, nil, fmt.Errorf("runner not ready: %s", runner.status)
	}

	runner.mu.RLock()
	pending, exists := runner.pending[req.ID]
	setupCompletedChan := runner.setupComplete
	runner.mu.RUnlock()
	if !exists {
		m.releaseSlot()
		return nil, nil, fmt.Errorf("failed to find pending prediction after allocation: %s", req.ID)
	}
	select {
	case <-setupCompletedChan:
		// We need to wait for setup to complete before proceeding so that we can ensure that
		// the OpenAPI schema is available for input processing
		log.Tracew("runner setup complete, proceeding with prediction", "prediction_id", req.ID, "runner", runner.runnerCtx.id)
	case <-pending.ctx.Done():
		// Prediction was canceled, watcher will perform cleanup, we need to abort
		// the rest of the prediction processing
		log.Tracew("prediction was canceled before setup complete, aborting", "prediction_id", req.ID, "runner", runner.runnerCtx.id)
		m.releaseSlot()
		return nil, nil, fmt.Errorf("prediction %s was canceled: %w", req.ID, pending.ctx.Err())
	}

	// Check for setup failure before calling predict
	runner.mu.Lock()
	status := runner.status
	runner.mu.Unlock()
	if status == StatusSetupFailed {
		// Setup failure will be handled by async webhook machinery
		// Return sentinel error to indicate async handling
		log.Tracew("setup failed, using async handling", "prediction_id", req.ID, "runner", runner.runnerCtx.id)
		m.releaseSlot()
		return nil, nil, ErrAsyncPrediction
	}

	respChan, initialResponse, err := runner.predict(req.ID)
	if err != nil {
		m.releaseSlot()
		return nil, nil, err
	}

	// Wrap the channel to release slot when prediction completes
	wrappedChan := make(chan PredictionResponse, 1)
	go func() {
		defer m.releaseSlot()
		resp := <-respChan
		wrappedChan <- resp
		close(wrappedChan)
	}()

	return wrappedChan, initialResponse, nil
}

// createDefaultRunner creates the default runner for non-procedure mode
func (m *Manager) createDefaultRunner(ctx context.Context) (*Runner, error) {
	log := m.logger.Sugar()

	workingDir := m.cfg.WorkingDirectory
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	log.Debugw("creating default runner",
		"working_dir", workingDir,
		"ipc_url", m.cfg.IPCUrl,
		"signal_mode", m.cfg.SignalMode,
		"python_bin", m.cfg.PythonBinPath,
	)

	pythonPath := "python3"
	if m.cfg.PythonBinPath != "" {
		pythonPath = m.cfg.PythonBinPath
	}

	args := []string{
		"-u",
		"-m", "coglet",
		"--name", DefaultRunnerName,
		"--working-dir", workingDir,
	}

	if m.cfg.SignalMode {
		args = append(args, "--signal-mode")
		// Make sure the signal handling is running
		// This runs an infinite loop for handling signals, so we explicitly
		// do not want to put it in a wait group of any kind
		go m.HandleSignals(m.ctx) //nolint:contextcheck // We want this to live for the lifetime of the manager
	} else {
		args = append(args, "--ipc-url", m.cfg.IPCUrl)
	}

	// This returns an object that does nothing if it is not enabled.
	cp := checkpointer.NewCheckpointer(ctx, m.logger.Sugar())
	err := cp.Prepare(ctx)
	if err != nil {
		cp.Disable()
	}

	log.Debugw("runner command", "python_path", pythonPath, "args", args, "working_dir", workingDir)

	tmpDir, err := os.MkdirTemp("", "cog-runner-tmp-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	env := mergeEnv(os.Environ(), m.cfg.EnvSet, m.cfg.EnvUnset)
	env = append(env, "TMPDIR="+tmpDir)

	// Ensure Python processes never receive trace level logs
	if logLevel := os.Getenv("COG_LOG_LEVEL"); logLevel == "trace" {
		env = append(env, "COG_LOG_LEVEL=debug")
	} else if logLevel := os.Getenv("LOG_LEVEL"); logLevel == "trace" {
		env = append(env, "LOG_LEVEL=debug")
	}

	// Read cog.yaml for runner configuration (capacity was already set in newManager)
	cogYaml, err := ReadCogYaml(workingDir)
	if err != nil {
		log.Warnw("failed to read cog.yaml, using default concurrency", "error", err)
		cogYaml = &CogYaml{Concurrency: CogConcurrency{Max: 1}}
	}

	var uploader *uploader
	if m.cfg.UploadURL != "" {
		uploader = newUploader(m.cfg.UploadURL)
	}

	runnerCtx := RunnerContext{
		id:         DefaultRunnerName,
		workingdir: workingDir,
		tmpDir:     tmpDir,
		uploader:   uploader,
	}

	// If there is an existing checkpoint, try to restore from the checkpoint
	if cp.HasCheckpoint() {
		runner, err := m.startRunnerFromCheckpoint(ctx, env, runnerCtx, cogYaml.Concurrency.Max, cp)
		if err == nil {
			m.runners[0] = runner
			m.monitoringWG.Go(func() {
				m.monitorRunnerSubprocess(m.ctx, DefaultRunnerName, runner)
			})

			return runner, cp.WriteReadyFile()
		}
		// If the error was non-nil, disable the checkpointer and continue
		cp.Disable()
	}

	// Derive the runtime context from the manager's context
	runtimeContext, runtimeCancel := context.WithCancel(ctx)

commandSetup:
	cmd := exec.CommandContext(runtimeContext, pythonPath, args...) //nolint:gosec // expected subprocess launched with variable
	runner, err := m.setupRunner(runtimeContext, runtimeCancel, cmd, env, runnerCtx, cogYaml.Concurrency.Max)
	if err != nil {
		return nil, fmt.Errorf("failed to set up runner: %w", err)
	}

	if err := runner.Config(ctx); err != nil {
		if stopErr := runner.Stop(); stopErr != nil {
			log.Errorw("failed to stop runner", "name", DefaultRunnerName, "error", stopErr)
		}

		return nil, fmt.Errorf("failed to config runner: %w", err)
	}

	m.runners[0] = runner

	if !cp.HasCheckpoint() {
		err = cp.Checkpoint(ctx, cmd, func() error { return waitForRunnerSetup(ctx, runner) })
		var FatalCheckpointError *checkpointer.FatalCheckpointError
		// If we saw an error that would leave the runner unusable, turn off the
		// checkpointer and recreate the command and runner
		if errors.As(err, &FatalCheckpointError) {
			// TODO: Is this bad? Should we just return the error back up?
			// The main concern is what `runner.Config` does leaving artifacts
			// between runs, although I think that should be fine?
			cp.Disable()
			goto commandSetup
		}
		// If the error is not fatal, we failed to create a checkpoint but are still
		// running the cog process successfully, so we can just continue as if we did
		// nothing
	}

	m.monitoringWG.Go(func() {
		m.monitorRunnerSubprocess(m.ctx, DefaultRunnerName, runner)
	})

	return runner, cp.WriteReadyFile()
}

func (m *Manager) setupRunner(runtimeContext context.Context, runtimeCancel context.CancelFunc, cmd *exec.Cmd, env []string, runnerCtx RunnerContext, maxConcurrency int) (*Runner, error) {
	cmd.Dir = m.cfg.WorkingDirectory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = env

	runner, err := NewRunner(runtimeContext, runtimeCancel, runnerCtx, cmd, maxConcurrency, m.cfg, m.baseLogger)
	if err != nil {
		return nil, err
	}

	runner.webhookSender = m.webhookSender
	if err := runner.Start(runtimeContext); err != nil {
		return nil, fmt.Errorf("failed to start runner: %w", err)
	}

	return runner, nil
}

func (m *Manager) startRunnerFromCheckpoint(ctx context.Context, env []string, runnerCtx RunnerContext, maxConcurrency int, cp checkpointer.Checkpointer) (*Runner, error) {
	// Derive the runtime context from the manager's context
	runtimeContext, runtimeCancel := context.WithCancel(ctx)

	cmd, postSetupCallback, err := cp.Restore(runtimeContext)
	if err != nil {
		runtimeCancel()
		return nil, err
	}

	runner, err := m.setupRunner(runtimeContext, runtimeCancel, cmd, env, runnerCtx, maxConcurrency)
	if err != nil {
		m.logger.Sugar().Errorw("failed to set up runner", "error", err)
		return nil, fmt.Errorf("failed to set up runner: %w", err)
	}

	err = postSetupCallback(runtimeContext)
	if err != nil {
		return nil, fmt.Errorf("failed callback function: %w", err)
	}

	// We checkpointed the model after it ran setup, so we need to manually send the ready signal
	// to the runner. We can do this by sending the SigReady signal to the current PID, as signal
	// mode should be on if the checkpoint exists
	err = syscall.Kill(syscall.Getpid(), SigReady)
	if err != nil {
		m.logger.Sugar().Errorw("failed to send SIGUSR1", "error", err)
	}

	return runner, err
}

// allocatePrediction reserves a slot in the runner for the prediction
func (m *Manager) allocatePrediction(runner *Runner, req PredictionRequest) { //nolint:contextcheck // we do not use this context for the prediction see note below
	log := m.logger.Sugar()
	runner.mu.Lock()
	defer runner.mu.Unlock()

	//  Derive context from manager so watcher survives runner crashes
	// NOTE(morgan): by design we do not use the passed in context, as the passed
	// in context is tied to the http request, and would cause the prediction to
	// fail at the end of the http request's lifecycle.
	predictionCtx, cancel := context.WithCancel(m.ctx)

	pending := &PendingPrediction{
		request:       req,
		outputCache:   make(map[string]string),
		c:             make(chan PredictionResponse, 1),
		cancel:        cancel, // Manager can cancel this watcher explicitly
		ctx:           predictionCtx,
		watcherDone:   make(chan struct{}),
		outputNotify:  make(chan struct{}, 1),
		webhookSender: m.webhookSender,
	}
	runner.pending[req.ID] = pending

	now := time.Now().Format(config.TimeFormat)
	if pending.request.CreatedAt == "" {
		pending.request.CreatedAt = now
	}
	if pending.request.StartedAt == "" {
		pending.request.StartedAt = now
	}

	// Start per-prediction response watcher with cleanup wrapper
	go func() {
		defer func() {
			// When watcher exits, handle terminal webhook and cleanup
			pending.mu.Lock()
			pending.response.populateFromRequest(pending.request)
			if err := pending.response.finalizeResponse(); err != nil {
				log.Errorw("failed to finalize response", "error", err)
			}

			// Send terminal webhook if prediction completed
			if err := m.sendTerminalWebhook(pending); err != nil {
				log.Errorw("failed to send terminal webhook", "error", err)
			}
			pending.mu.Unlock()

			// Remove from pending map and cancel context
			runner.mu.Lock()
			delete(runner.pending, req.ID)
			runner.mu.Unlock()

			// In one-shot mode, stop runner after prediction completes to trigger cleanup
			if m.cfg.OneShot {
				go func() {
					logger := m.logger.Sugar()
					logger.Infow("one-shot mode: stopping runner after prediction completion", "prediction_id", req.ID, "runner_id", runner.runnerCtx.id)

					// Try graceful stop with timeout
					stopDone := make(chan error, 1)
					go func() {
						stopDone <- runner.Stop()
					}()

					timeout := m.cfg.CleanupTimeout
					if timeout == 0 {
						timeout = 10 * time.Second // Default timeout
					}

					select {
					case err := <-stopDone:
						if err != nil {
							logger.Errorw("failed to stop runner in one-shot mode", "error", err, "runner_id", runner.runnerCtx.id)
						}
						runner.ForceKill()
					case <-time.After(timeout):
						logger.Warnw("stop timeout exceeded in one-shot mode, falling back to force kill", "timeout", timeout, "runner_id", runner.runnerCtx.id)
						runner.ForceKill()
					}
				}()
			}

			if cancel != nil {
				cancel()
			}
		}()

		runner.watchPredictionResponses(predictionCtx, req.ID, pending)
	}()
}

func (m *Manager) assignReqToRunner(ctx context.Context, req PredictionRequest, async bool) (*Runner, error) {
	log := m.logger.Sugar()

	if !m.cfg.UseProcedureMode {
		procRunner, _, exists := m.findRunner(DefaultRunnerName)
		if !exists {
			var err error
			procRunner, err = m.createDefaultRunner(ctx)
			if err != nil {
				return nil, err
			}
		}
		// NOTE(morgan): we do not use the http request's context for the prediction
		// to allow us to derive the context from the manager's context, ensuring the context
		// lifecycle is not tied to the http request's lifetime.
		m.allocatePrediction(procRunner, req) //nolint:contextcheck // see above note
		return procRunner, nil

	}

	procSrcURL := req.ProcedureSourceURL
	// First, try to find existing runner with capacity and atomically reserve slot
	procRunner := m.findRunnerWithCapacity(ctx, req)
	if procRunner != nil {
		log.Tracew("allocated request to existing runner", "runner", procRunner.runnerCtx.id)
		return procRunner, nil
	}

	m.mu.Lock()
	// No existing runner with capacity, need to create new one
	// Allocate a runner slot (find empty slot or evict idle runner) and create runner
	// NOTE(morgan): we do not use the http request's context for the prediction
	// to allow us to derive the context from the manager's context, ensuring the context
	// lifecycle is not tied to the http request's lifetime.
	procRunner, err := m.allocateRunnerSlot(procSrcURL) //nolint:contextcheck // we do not use the http request's context for the prediction by design
	if err != nil {
		return nil, err
	}

	if err := procRunner.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start runner: %w", err)
	}

	// Start monitoring before config - crashes happen when Python tries to load procedure after config
	m.monitoringWG.Go(func() {
		m.monitorRunnerSubprocess(m.ctx, procRunner.runnerCtx.id, procRunner)
	})

	if err := procRunner.Config(ctx); err != nil {
		if stopErr := procRunner.Stop(); stopErr != nil {
			log.Errorw("failed to stop runner", "name", procRunner.runnerCtx.id, "error", stopErr)
		}
		return nil, fmt.Errorf("failed to config runner: %w", err)
	}

	// Pre-allocate prediction for the new runner
	// NOTE(morgan): we do not use the http request's context for the prediction
	// to allow us to derive the context from the manager's context, ensuring the context
	// lifecycle is not tied to the http request's lifetime.
	m.allocatePrediction(procRunner, req) //nolint:contextcheck // see above note
	m.mu.Unlock()

	if !async {
		if err := waitForRunnerSetup(ctx, procRunner); err != nil {
			return nil, err
		}
	}
	return procRunner, nil
}

func waitForRunnerSetup(ctx context.Context, runner *Runner) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for runner setup: %w", ctx.Err())
	case <-runner.setupComplete:
		// Setup complete, runner is ready (or failed, but we continue anyway for async)
	}
	return nil
}

// findRunnerWithCapacity looks for existing runner with matching procedure hash and atomically reserves capacity
func (m *Manager) findRunnerWithCapacity(ctx context.Context, req PredictionRequest) *Runner {
	m.mu.Lock()
	defer m.mu.Unlock()

	procedureHash := req.ProcedureSourceURL

	for _, runner := range m.runners {
		if runner != nil && runner.procedureHash == procedureHash {
			runner.mu.Lock()
			// Check that runner is ready and has capacity
			if len(runner.pending) < runner.maxConcurrency {
				runner.mu.Unlock()
				// Reserve slot by pre-allocating prediction
				// NOTE(morgan): we do not use the http request's context for the prediction
				// to allow us to derive the context from the manager's context, ensuring the context
				// lifecycle is not tied to the http request's lifetime.
				m.allocatePrediction(runner, req) //nolint:contextcheck // see above note
				return runner
			}
			runner.mu.Unlock()
		}
	}
	return nil
}

func (m *Manager) allocateRunnerSlot(procedureHash string) (*Runner, error) {
	log := m.logger.Sugar()

	// Generate unique runner name
	var runnerName string
	for {
		name := GenerateRunnerID().String()
		if _, _, exists := m.findRunner(name); !exists {
			runnerName = name
			break
		}
	}

	// Check if there's an empty slot
	if slot, err := m.findEmptySlot(); err == nil {
		// Found empty slot, create and place runner
		runner, err := m.createProcedureRunner(runnerName, procedureHash)
		if err != nil {
			return nil, err
		}
		m.runners[slot] = runner
		return runner, nil
	}

	// No empty slots, try to evict an idle runner or defunct runner
	for i, runner := range m.runners {
		if runner != nil && ((runner.status == StatusReady && runner.Idle()) || runner.status == StatusDefunct) {
			log.Debugw("evicting idle runner", "name", runner.runnerCtx.id)
			err := runner.Stop()
			if err != nil {
				log.Errorw("failed to stop runner", "name", runner.runnerCtx.id, "error", err)
			}
			// Create new runner and place in slot
			newRunner, err := m.createProcedureRunner(runnerName, procedureHash)
			if err != nil {
				return nil, err
			}
			m.runners[i] = newRunner
			return newRunner, nil
		}
	}

	return nil, ErrNoEmptySlot
}

// shouldUseSetUID determines if setUID isolation should be used for procedure runners
func (m *Manager) shouldUseSetUID() bool {
	if !m.cfg.UseProcedureMode {
		return false
	}

	// Check if running in Docker or K8s
	_, err := os.Stat("/.dockerenv")
	inDocker := err == nil
	_, inK8S := os.LookupEnv("KUBERNETES_SERVICE_HOST")

	// Only use setUID if running as root in Docker or K8s
	return (inDocker || inK8S) && os.Getuid() == 0
}

func (m *Manager) createProcedureRunner(runnerName, procedureHash string) (*Runner, error) {
	log := m.logger.Sugar()

	// Prepare procedure source by copying files to working directory
	workingDir, err := PrepareProcedureSourceURL(procedureHash, runnerName)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare procedure source: %w", err)
	}

	// Create subprocess command with proper env merging
	pythonPath := "python3"
	if m.cfg.PythonBinPath != "" {
		pythonPath = m.cfg.PythonBinPath
	}

	args := []string{
		"-u",
		"-m", "coglet",
		"--name", runnerName,
		"--ipc-url", m.cfg.IPCUrl,
		"--working-dir", workingDir,
	}

	tmpDir, err := os.MkdirTemp("", "cog-runner-tmp-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Derive the runtime context from the manager's context
	runtimeContext, runtimeCancel := context.WithCancel(m.ctx)
	cmd := exec.CommandContext(runtimeContext, pythonPath, args...) //nolint:gosec // expected subprocess launched with variable
	cmd.Dir = workingDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	env := mergeEnv(os.Environ(), m.cfg.EnvSet, m.cfg.EnvUnset)
	env = append(env, "TMPDIR="+tmpDir)

	// Ensure Python processes never receive trace level logs
	if logLevel := os.Getenv("COG_LOG_LEVEL"); logLevel == "trace" {
		env = append(env, "COG_LOG_LEVEL=debug")
	} else if logLevel := os.Getenv("LOG_LEVEL"); logLevel == "trace" {
		env = append(env, "LOG_LEVEL=debug")
	}

	cmd.Env = env

	var allocatedUID *int
	if m.shouldUseSetUID() {
		uid, err := AllocateUID()
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to allocate UID: %w", err)
		}
		allocatedUID = &uid

		// Use os.Root for secure ownership changes
		workingRoot, err := os.OpenRoot(workingDir)
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to open working directory root: %w", err)
		}
		defer func() { _ = workingRoot.Close() }()

		err = fs.WalkDir(workingRoot.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if lchownErr := workingRoot.Lchown(path, uid, NoGroupGID); lchownErr != nil {
				log.Errorw("failed to change ownership", "path", path, "uid", uid, "error", lchownErr)
				return lchownErr
			}
			return nil
		})
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of source directory: %w", err)
		}

		if err := workingRoot.Lchown(".", uid, NoGroupGID); err != nil {
			log.Errorw("failed to change ownership of working directory", "path", workingDir, "uid", uid, "error", err)
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of working directory: %w", err)
		}

		tmpRoot, err := os.OpenRoot(tmpDir)
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to open temp directory root: %w", err)
		}
		defer func() { _ = tmpRoot.Close() }()

		if err := tmpRoot.Lchown(".", uid, NoGroupGID); err != nil {
			log.Errorw("failed to change ownership of temp directory", "path", tmpDir, "uid", uid, "error", err)
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of temp directory: %w", err)
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: uint32(uid), //nolint:gosec // this is guarded in isolation .allocate, cannot exceed const MaxUID
			Gid: uint32(NoGroupGID),
		}
	}
	// Procedures don't have cog.yaml, use default concurrency

	// Create runner context and runner
	var uploader *uploader
	if m.cfg.UploadURL != "" {
		uploader = newUploader(m.cfg.UploadURL)
	}

	runnerCtx := RunnerContext{
		id:                 runnerName,
		workingdir:         workingDir,
		tmpDir:             tmpDir,
		uploader:           uploader,
		uid:                allocatedUID,
		cleanupDirectories: m.cfg.CleanupDirectories,
	}

	runner, err := NewRunner(runtimeContext, runtimeCancel, runnerCtx, cmd, 1, m.cfg, m.baseLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}
	runner.webhookSender = m.webhookSender
	// Procedure-specific setup
	runner.procedureHash = procedureHash

	return runner, nil
}

// GetRunner returns a runner by name

// Runners returns a list of all active runners
func (m *Manager) Runners() []*Runner {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.runners
}

// findRunner finds a runner by name in the slice
func (m *Manager) findRunner(name string) (*Runner, int, bool) {
	for i, runner := range m.runners {
		if runner != nil && runner.runnerCtx.id == name {
			return runner, i, true
		}
	}
	return nil, -1, false
}

// findEmptySlot finds the first empty slot in the runners slice
func (m *Manager) findEmptySlot() (int, error) {
	for i, runner := range m.runners {
		if runner == nil {
			return i, nil
		}
	}
	return -1, ErrNoEmptySlot
}

// Capacity returns the number of available capacity slots
func (m *Manager) Capacity() int {
	return len(m.capacity)
}

// AvailableCapacity returns the number of available capacity slots
func (m *Manager) AvailableCapacity() int {
	return len(m.capacity)
}

// Stop gracefully shuts down all runners
func (m *Manager) Stop() error {
	var stopErr error
	m.stopOnce.Do(func() {
		log := m.logger.Sugar()
		log.Info("stopping runner manager")

		m.mu.Lock()
		runnerList := make([]*Runner, 0, len(m.runners))
		for _, runner := range m.runners {
			if runner != nil {
				runnerList = append(runnerList, runner)
			}
		}
		m.mu.Unlock()

		// Signal all runners for graceful shutdown
		for _, runner := range runnerList {
			runner.GracefulShutdown()
		}

		// Wait for runners to become idle or timeout using WaitGroup
		gracePeriod := m.cfg.RunnerShutdownGracePeriod
		log.Debugw("grace period configuration", "grace_period", gracePeriod)
		graceCtx, cancel := context.WithTimeout(m.ctx, gracePeriod)
		defer cancel()

		var wg sync.WaitGroup
		for _, runner := range runnerList {
			wg.Go(func() {
				log.Debugw("waiting for runner to become idle", "name", runner.runnerCtx.id, "grace_period", gracePeriod)
				// Wait for this runner to become idle OR timeout
				select {
				case <-runner.readyForShutdown:
					log.Debugw("runner became idle naturally", "name", runner.runnerCtx.id)
				case <-graceCtx.Done():
					log.Warnw("grace period expired for runner", "name", runner.runnerCtx.id, "context_err", graceCtx.Err())
				}

				// Always try to stop, handle errors independently
				if err := runner.Stop(); err != nil {
					log.Errorw("failed to stop runner gracefully", "name", runner.runnerCtx.id, "error", err)
				}
			})
		}

		// Wait for all runners to complete shutdown (success or failure)
		wg.Wait()

		log.Info("all runners stopped successfully")
		close(m.stopped)
	})

	return stopErr
}

// IsStopped returns whether the manager has been stopped
func (m *Manager) IsStopped() bool {
	select {
	case <-m.stopped:
		return true
	default:
		return false
	}
}

// Concurrency returns semaphore-based concurrency info
func (m *Manager) Concurrency() Concurrency {
	return Concurrency{
		Max:     cap(m.capacity),
		Current: cap(m.capacity) - len(m.capacity),
	}
}

// Status returns the overall system status
func (m *Manager) Status() string {
	log := m.logger.Sugar()
	concurrency := m.Concurrency()

	if !m.cfg.UseProcedureMode {
		// Single runner mode - check if default runner exists and is ready
		if runner, _, exists := m.findRunner(DefaultRunnerName); exists {
			runner.mu.Lock()
			status := runner.status.String()
			runner.mu.Unlock()
			return status
		}
		log.Trace("default runner not found, returning STARTING")
		return "STARTING"
	}

	// Procedure mode - determine status based on capacity
	if concurrency.Current < concurrency.Max && !m.cleanupInProgress() {
		return "READY"
	}
	return "BUSY"
}

// SetupResult returns setup result for health checks
func (m *Manager) SetupResult() SetupResult {
	if !m.cfg.UseProcedureMode {
		// Single runner mode - return default runner's setup result
		if runner, _, exists := m.findRunner(DefaultRunnerName); exists {
			runner.mu.Lock()
			defer runner.mu.Unlock()
			return runner.setupResult
		}
		return SetupResult{Status: SetupFailed}
	}

	// Procedure mode - synthetic setup result
	return SetupResult{
		Status: SetupSucceeded,
	}
}

// ExitCode returns exit code for non-procedure mode
func (m *Manager) ExitCode() int {
	if m.cfg.UseProcedureMode {
		return 0
	}
	if runner, _, exists := m.findRunner(DefaultRunnerName); exists {
		if runner.cmd.ProcessState != nil {
			return runner.cmd.ProcessState.ExitCode()
		}
	}
	return 0
}

func (m *Manager) CancelPrediction(predictionID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, runner := range m.runners {
		if err := runner.Cancel(predictionID); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrPredictionNotFound, predictionID)
}

func (m *Manager) HandleRunnerIPC(runnerName, status string) error {
	runner, _, exists := m.findRunner(runnerName)
	if !exists {
		return fmt.Errorf("%w: %s", ErrRunnerNotFound, runnerName)
	}
	return runner.HandleIPC(status)
}

func (m *Manager) HandleRunnerSignal(runnerName string, s os.Signal) error {
	runner, _, exists := m.findRunner(runnerName)
	if !exists {
		return fmt.Errorf("%w: %s", ErrRunnerNotFound, runnerName)
	}
	return runner.HandleSignal(s)
}

func (m *Manager) HandleSignals(ctx context.Context) {
	log := m.logger.Sugar()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, SigOutput, SigReady, SigBusy)

	for {
		select {
		case s := <-ch:
			err := m.HandleRunnerSignal(DefaultRunnerName, s)
			if err != nil {
				log.Errorw("failed to handle IPC", "signal", s, "error", err)
				// TODO: What do we do with this error? Put it on some error chan
				// and ship it somewhere?
			}
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) cleanupInProgress() bool {
	if !m.cfg.OneShot {
		return false
	}

	// Check if any runners are in cleanup
	for _, runner := range m.runners {
		if runner != nil && len(runner.cleanupSlot) == 0 {
			return true
		}
	}
	return false
}

// Schema returns the appropriate schema - procedure schema for procedure mode, runner schema for non-procedure mode
func (m *Manager) Schema() (string, bool) {
	if m.cfg.UseProcedureMode {
		return procedureSchema, true
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if runner, _, exists := m.findRunner(DefaultRunnerName); exists {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		if runner.schema == "" {
			return "", false // Schema not ready
		}
		return runner.schema, true
	}
	return "", false // No runner available
}

// ForceKillAll immediately force-kills all runners and waits briefly for cleanup
func (m *Manager) ForceKillAll() {
	m.mu.Lock()
	runners := make([]*Runner, 0, len(m.runners))
	for _, runner := range m.runners {
		if runner != nil {
			runners = append(runners, runner)
		}
	}
	m.mu.Unlock()

	// Kill all runners in parallel for faster shutdown
	var killWG sync.WaitGroup
	for _, runner := range runners {
		killWG.Go(func() {
			runner.ForceKill()
		})
	}
	killWG.Wait()

	// Wait briefly for monitoring goroutines to complete cleanup
	// This ensures last logs are captured and predictions are properly failed
	// before the process exits, which is critical for reliable error reporting
	done := make(chan struct{})
	go func() {
		m.monitoringWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All monitoring completed cleanly
	case <-time.After(200 * time.Millisecond):
		// Timeout - continue anyway to avoid hanging
		m.logger.Warn("ForceKillAll timed out waiting for monitoring cleanup")
	}
}

func (m *Manager) monitorRunnerSubprocess(ctx context.Context, runnerName string, runner *Runner) {
	log := m.logger.Sugar().With("runner_name", runnerName)

	cmd, err := runner.getCmd()
	if err != nil {
		log.Errorw("failed to get command for subprocess monitoring", "error", err)
		return
	}

	err = cmd.Wait()

	select {
	case <-ctx.Done():
		return
	default:
		log.Debugw("subprocess exited", "pid", cmd.Process.Pid, "error", err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()

	select {
	case <-runner.logCaptureComplete:
		// log capture complete
	case <-time.After(1000 * time.Millisecond):
		// if log capture isn't completed within 1 second, we continue on
		// it's better to capture what we have rather than hanging predictions
		// when we need to fail them.
		log.Debug("log capture not marked as complete during crash, continuing")
	}

	// Evict the failed runner from manager slot immediately after log capture
	// In procedure mode, this releases the slot for new runners while we handle prediction failures
	// In non-procedure mode, we keep the runner but mark it as defunct
	if m.cfg.UseProcedureMode {
		m.mu.Lock()
		for i, r := range m.runners {
			if r == runner {
				m.runners[i] = nil
				break
			}
		}
		m.mu.Unlock()
	}

	if runner.status == StatusStarting {
		log.Debugw("subprocess exited during startup, checking setup result")

		// Handle setup failure - update both runner status and setup result
		runner.status = StatusSetupFailed
		runner.setupResult.Status = SetupFailed

		// Close setupComplete to unblock waiting allocation
		select {
		case <-runner.setupComplete:
			// Already closed
		default:
			close(runner.setupComplete)
		}

		// Capture crash logs from runner and fail predictions one by one
		log.Tracew("checking runner logs for crash", "runner_logs_count", len(runner.logs), "runner_logs", runner.logs)
		crashLogs := runner.logs
		log.Tracew("captured crash logs", "crash_logs_count", len(crashLogs), "crash_logs", crashLogs)

		for id, pending := range runner.pending {
			log.Debugw("failing prediction due to setup failure", "prediction_id", id)

			// Add crash logs to this prediction and fail it immediately
			pending.mu.Lock()
			if pending.response.Logs == nil {
				pending.response.Logs = make([]string, 0)
			}
			pending.response.Logs = append(pending.response.Logs, crashLogs...)
			allLogs := pending.response.Logs
			pending.mu.Unlock()

			failedResponse := PredictionResponse{
				Status:  PredictionFailed,
				Error:   "setup failed",
				Logs:    allLogs,
				Metrics: pending.response.Metrics,
			}
			failedResponse.populateFromRequest(pending.request)

			pending.safeSend(failedResponse)
			pending.safeClose()

			// Update pending response with failed response for webhook
			pending.mu.Lock()
			pending.response = failedResponse

			// Send terminal webhook since we're canceling the watcher
			if err := m.sendTerminalWebhook(pending); err != nil {
				log.Errorw("failed to send terminal webhook", "error", err)
			}
			pending.mu.Unlock()

			for _, inputPath := range pending.inputPaths {
				if err := os.Remove(inputPath); err != nil {
					log.Errorw("failed to remove input path", "path", inputPath, "error", err)
				}
			}

			// Cancel the prediction's response watcher
			if pending.cancel != nil {
				pending.cancel()
			}
		}

		runner.pending = make(map[string]*PendingPrediction)
		return
	}

	if runner.status == StatusReady || runner.status == StatusBusy {
		log.Debugw("subprocess crashed during prediction execution, failing pending predictions")

		crashLogs := runner.logs

		for id, pending := range runner.pending {
			log.Debugw("failing prediction due to subprocess crash", "prediction_id", id)

			// Add crash logs to this prediction and fail it immediately
			pending.mu.Lock()
			if pending.response.Logs == nil {
				pending.response.Logs = make([]string, 0)
			}
			pending.response.Logs = append(pending.response.Logs, crashLogs...)
			allLogs := pending.response.Logs
			pending.mu.Unlock()

			failedResponse := PredictionResponse{
				Status:  PredictionFailed,
				Error:   "prediction failed",
				Logs:    allLogs,
				Metrics: pending.response.Metrics,
			}
			failedResponse.populateFromRequest(pending.request)

			pending.safeSend(failedResponse)
			pending.safeClose()

			// Update pending response with failed response for webhook
			pending.mu.Lock()
			pending.response = failedResponse

			// Send terminal webhook since we're canceling the watcher
			if err := m.sendTerminalWebhook(pending); err != nil {
				log.Errorw("failed to send terminal webhook", "error", err)
			}
			pending.mu.Unlock()

			for _, inputPath := range pending.inputPaths {
				if err := os.Remove(inputPath); err != nil {
					log.Errorw("failed to remove input path", "path", inputPath, "error", err)
				}
			}

			// Cancel the prediction's response watcher
			if pending.cancel != nil {
				pending.cancel()
			}
		}

		runner.pending = make(map[string]*PendingPrediction)
		runner.status = StatusDefunct
	}
}

func (m *Manager) sendTerminalWebhook(pending *PendingPrediction) error {
	log := m.logger.Sugar()
	// Send terminal webhook since we're canceling the watcher
	if pending.response.Status.IsCompleted() && pending.terminalWebhookSent.CompareAndSwap(false, true) {
		if err := pending.response.finalizeResponse(); err != nil {
			log.Errorw("failed to finalize response", "error", err)
		}
		return pending.sendWebhookSync(webhook.EventCompleted)
	}
	return nil
}
