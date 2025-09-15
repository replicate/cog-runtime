package runner

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/replicate/cog-runtime/internal/config"
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

	baseLogger *zap.Logger // base logger passed from parent, used to create named loggers for runners
	logger     *zap.Logger
}

// NewManager creates a new runner manager with channel-based capacity control
func NewManager(ctx context.Context, cfg config.Config, logger *zap.Logger) *Manager {
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

func newManager(ctx context.Context, cfg config.Config, logger *zap.Logger) *Manager {
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
					logger.Info("read concurrency from cog.yaml", zap.Int("max_concurrency", maxRunners))
				}
			}
		} else {
			cogYaml, err := ReadCogYaml(workingDir)
			if err != nil {
				logger.Warn("failed to read cog.yaml, using default concurrency", zap.Error(err))
				maxRunners = 1
			} else {
				maxRunners = max(1, cogYaml.Concurrency.Max)
				logger.Info("read concurrency from cog.yaml", zap.Int("max_concurrency", maxRunners))
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
		return nil
	default:
		return ErrNoCapacity
	}
}

func (m *Manager) releaseSlot() {
	select {
	case m.capacity <- struct{}{}:
	default:
		m.logger.Warn("attempted to release slot but channel is full")
	}
}

// Predict executes a sync prediction request - blocks until complete
func (m *Manager) Predict(req PredictionRequest) (*PredictionResponse, error) {
	respChan, err := m.predict(m.ctx, req)
	if err != nil {
		return nil, err
	}

	// Wait for completion and return result
	resp := <-respChan
	return &resp, nil
}

// PredictAsync executes an async prediction request - returns immediately, sends webhook when complete
func (m *Manager) PredictAsync(ctx context.Context, req PredictionRequest) error {
	log := m.logger.Sugar()
	if err := m.claimSlot(); err != nil {
		return err
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 10*time.Second)
	defer deadlineCancel()

	runner, err := m.assignReqToRunner(deadlineCtx, req)
	if err != nil {
		log.Debugw("failed to get runner for async request", "error", err)
		m.releaseSlot()
		return err
	}

	switch runner.status {
	case StatusReady:
		// Status ready is always valid for new predictions
		break
	case StatusStarting:
		if !m.cfg.UseProcedureMode {
			m.releaseSlot()
			return fmt.Errorf("%w: %s", ErrInvalidRunnerStatus, runner.status)
		}
	default:
		m.releaseSlot()
		return fmt.Errorf("%w: %s", ErrInvalidRunnerStatus, runner.status)
	}

	respChan, err := runner.predict(req)
	if err != nil {
		log.Debugw("failed to predict", "error", err)
		m.releaseSlot()
		return err
	}

	// Release slot when prediction completes in background
	go func() {
		defer m.releaseSlot() // Release slot after prediction completes
		<-respChan            // Wait for prediction to complete
		log.Debugw("async prediction completed", "prediction_id", req.ID)
	}()

	return nil
}

// predict is the internal implementation shared by both sync and async predictions
func (m *Manager) predict(ctx context.Context, req PredictionRequest) (chan PredictionResponse, error) {
	if err := m.claimSlot(); err != nil {
		return nil, err
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 10*time.Second)
	defer deadlineCancel()

	runner, err := m.assignReqToRunnerWait(deadlineCtx, req)
	if err != nil {
		m.releaseSlot()
		return nil, err
	}

	if !m.cfg.UseProcedureMode && runner.status != StatusReady {
		m.releaseSlot()
		return nil, fmt.Errorf("runner not ready: %s", runner.status)
	}

	respChan, err := runner.predict(req)
	if err != nil {
		m.releaseSlot()
		return nil, err
	}

	// Wrap the channel to release slot when prediction completes
	wrappedChan := make(chan PredictionResponse, 1)
	go func() {
		defer m.releaseSlot()
		resp := <-respChan
		wrappedChan <- resp
		close(wrappedChan)
	}()

	return wrappedChan, nil
}

// sendTerminalWebhook sends a terminal webhook synchronously for a completed prediction
func (m *Manager) sendTerminalWebhook(req PredictionRequest, resp PredictionResponse) error {
	log := m.logger.Sugar()

	// Send synchronously using SendConditional to respect filters
	// Send the actual PredictionResponse object, not a custom map

	body, err := json.Marshal(resp)
	if err != nil {
		log.Errorw("failed to marshal prediction response", "error", err)
		return fmt.Errorf("failed to marshal prediction response: %w", err)
	}

	if err := m.webhookSender.SendConditional(req.Webhook, bytes.NewReader(body), webhook.EventCompleted, req.WebhookEventsFilter, nil); err != nil {
		log.Errorw("failed to send terminal webhook", "prediction_id", resp.ID, "webhook_url", req.Webhook, "error", err)
		return fmt.Errorf("failed to send terminal webhook: %w", err)
	}
	log.Infow("sent terminal webhook", "prediction_id", resp.ID, "webhook_url", req.Webhook, "status", resp.Status)
	return nil
}

func (m *Manager) assignReqToRunnerWait(ctx context.Context, req PredictionRequest) (*Runner, error) {
	runner, err := m.assignReqToRunner(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to assign request to runner: %w", err)
	}
	if waitForRunnerSetup(ctx, runner) != nil {
		return nil, err
	}
	return runner, nil
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

	log.Infow("creating default runner",
		"working_dir", workingDir,
		"ipc_url", m.cfg.IPCUrl,
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
		"--ipc-url", m.cfg.IPCUrl,
		"--working-dir", workingDir,
	}

	log.Infow("runner command", "python_path", pythonPath, "args", args, "working_dir", workingDir)

	tmpDir, err := os.MkdirTemp("", "cog-runner-tmp-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Derive the runtime context from the manager's context
	runtimeContext, runtimeCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runtimeContext, pythonPath, args...) //nolint:gosec // expected subprocess launched with variable
	cmd.Dir = m.cfg.WorkingDirectory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	env := mergeEnv(os.Environ(), m.cfg.EnvSet, m.cfg.EnvUnset)
	env = append(env, "TMPDIR="+tmpDir)
	cmd.Env = env

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
	runner, err := NewRunner(runtimeContext, runtimeCancel, runnerCtx, cmd, cogYaml.Concurrency.Max, m.baseLogger)
	if err != nil {
		return nil, err
	}

	runner.webhookSender = m.webhookSender
	if err := runner.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start runner: %w", err)
	}

	if err := runner.Config(ctx); err != nil {
		if stopErr := runner.Stop(); stopErr != nil {
			log.Errorw("failed to stop runner", "name", DefaultRunnerName, "error", stopErr)
		}

		return nil, fmt.Errorf("failed to config runner: %w", err)
	}

	m.runners[0] = runner
	m.monitoringWG.Go(func() {
		m.monitorRunnerSubprocess(m.ctx, DefaultRunnerName, runner)
	})

	return runner, nil
}

// allocatePrediction reserves a slot in the runner for the prediction
func (m *Manager) allocatePrediction(runner *Runner, req PredictionRequest) { //nolint:contextcheck // we do not use this context for the prediction see note below
	runner.mu.Lock()
	defer runner.mu.Unlock()

	//  Derive context from manager so watcher survives runner crashes
	// NOTE(morgan): by design we do not use the passed in context, as the passed
	// in context is tied to the http request, and would cause the prediction to
	// fail at the end of the http request's lifecycle.
	watcherCtx, cancel := context.WithCancel(m.ctx)

	pending := &PendingPrediction{
		request:       req,
		outputCache:   make(map[string]string),
		c:             make(chan PredictionResponse, 1),
		cancel:        cancel, // Manager can cancel this watcher explicitly
		watcherDone:   make(chan struct{}),
		outputNotify:  make(chan struct{}, 1),
		webhookSender: m.webhookSender,
	}
	runner.pending[req.ID] = pending

	// Start per-prediction response watcher with cleanup wrapper
	go func() {
		defer func() {
			// When watcher exits, handle terminal webhook and cleanup
			pending.mu.Lock()
			finalResponse := pending.response

			// Send terminal webhook if prediction completed
			if finalResponse.Status.IsCompleted() && pending.terminalWebhookSent.CompareAndSwap(false, true) {
				_ = m.sendTerminalWebhook(pending.request, finalResponse)
			}
			pending.mu.Unlock()

			// Remove from pending map and cancel context
			runner.mu.Lock()
			delete(runner.pending, req.ID)
			runner.mu.Unlock()

			if cancel != nil {
				cancel()
			}
		}()

		runner.watchPredictionResponses(watcherCtx, req.ID, pending)
	}()
}

func (m *Manager) assignReqToRunner(ctx context.Context, req PredictionRequest) (*Runner, error) {
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
		log.Debugw("allocated request to existing runner", "runner", procRunner.runnerCtx.id)
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
			log.Infow("evicting idle runner", "name", runner.runnerCtx.id)
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
	cmd.Env = env

	// Apply setUID isolation for procedure runners if needed
	if m.shouldUseSetUID() {
		uid, err := AllocateUID()
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to allocate UID: %w", err)
		}

		// Change ownership of source directory (workingDir)
		err = filepath.WalkDir(workingDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if lchownErr := os.Lchown(path, uid, NoGroupGID); lchownErr != nil {
				log.Errorw("failed to change ownership", "path", path, "uid", uid, "error", lchownErr)
				return lchownErr
			}
			return nil
		})
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of source directory: %w", err)
		}

		// Make working dir writable by unprivileged Python process
		if err := os.Lchown(workingDir, uid, NoGroupGID); err != nil {
			log.Errorw("failed to change ownership of working directory", "path", workingDir, "uid", uid, "error", err)
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of working directory: %w", err)
		}
		// Change ownership of temp directory
		if err := os.Lchown(tmpDir, uid, NoGroupGID); err != nil {
			log.Errorw("failed to change ownership of temp directory", "path", tmpDir, "uid", uid, "error", err)
			runtimeCancel()
			return nil, fmt.Errorf("failed to change ownership of temp directory: %w", err)
		}
		// Use syscall.Credential to run process as unprivileged user from start
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
		id:         runnerName,
		workingdir: workingDir,
		tmpDir:     tmpDir,
		uploader:   uploader,
	}

	runner, err := NewRunner(runtimeContext, runtimeCancel, runnerCtx, cmd, 1, m.baseLogger)
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
		defer m.mu.Unlock()

		// Stop all runners
		for i, runner := range m.runners {
			if runner != nil {
				log.Infow("stopping runner", "name", runner.runnerCtx.id, "slot", i)
				if err := runner.Stop(); err != nil {
					log.Errorw("error stopping runner", "name", runner.runnerCtx.id, "error", err)
					if stopErr == nil {
						stopErr = err
					}
				}
			}
		}

		// Wait for runners to stop concurrently
		eg := errgroup.Group{}
		for i, runner := range m.runners {
			if runner != nil {
				name := runner.runnerCtx.id
				eg.Go(func() error {
					log.Infow("waiting for runner to stop", "name", name, "slot", i)
					runner.WaitForStop()
					return nil
				})
			}
		}

		if err := eg.Wait(); err != nil {
			log.Errorw("error waiting for runners to stop", "error", err)
			if stopErr == nil {
				stopErr = err
			}
		} else {
			log.Info("all runners stopped successfully")
		}

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
		log.Debug("default runner not found, returning STARTING")
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
		log.Debugw("checking runner logs for crash", "runner_logs_count", len(runner.logs), "runner_logs", runner.logs)
		crashLogs := runner.logs
		log.Debugw("captured crash logs", "crash_logs_count", len(crashLogs), "crash_logs", crashLogs)

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
				ID:      id,
				Status:  PredictionFailed,
				Input:   pending.request.Input,
				Error:   "setup failed",
				Logs:    allLogs,
				Metrics: pending.response.Metrics,
			}

			pending.safeSend(failedResponse)
			pending.safeClose()

			// Update pending response with failed response for webhook
			pending.mu.Lock()
			pending.response = failedResponse
			pending.mu.Unlock()

			// Send terminal webhook since we're canceling the watcher
			if pending.terminalWebhookSent.CompareAndSwap(false, true) {
				_ = pending.sendWebhookSync(webhook.EventCompleted)
			}

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
				ID:      id,
				Status:  PredictionFailed,
				Input:   pending.request.Input,
				Error:   "prediction failed",
				Logs:    allLogs,
				Metrics: pending.response.Metrics,
			}

			pending.safeSend(failedResponse)
			pending.safeClose()

			// Update pending response with failed response for webhook
			pending.mu.Lock()
			pending.response = failedResponse
			pending.mu.Unlock()

			// Send terminal webhook since we're canceling the watcher
			if pending.terminalWebhookSent.CompareAndSwap(false, true) {
				_ = pending.sendWebhookSync(webhook.EventCompleted)
			}

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
