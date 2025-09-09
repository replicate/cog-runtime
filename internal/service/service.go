package service

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// Config holds the configuration for the Service
type Config struct {
	Host                      string
	Port                      int
	UseProcedureMode          bool
	AwaitExplicitShutdown     bool
	OneShot                   bool
	UploadURL                 string
	WorkingDirectory          string
	RunnerShutdownGracePeriod time.Duration
	CleanupTimeout            time.Duration
	MaxRunners                int
	PythonBinPath             string
}

// Service is the root lifecycle owner for the cog runtime
type Service struct {
	cfg Config
	
	// Lifecycle state
	started         chan struct{}
	stopped         chan struct{}
	shutdown        chan struct{}
	shutdownStarted atomic.Bool

	logger *zap.Logger
}

// New creates a new Service with the given configuration
func New(cfg Config, baseLogger *zap.Logger) *Service {
	return &Service{
		cfg:      cfg,
		started:  make(chan struct{}),
		stopped:  make(chan struct{}),
		shutdown: make(chan struct{}),
		logger:   baseLogger.Named("service"),
	}
}

// Run starts the service and blocks until shutdown
func (s *Service) Run(ctx context.Context) error {
	log := s.logger.Sugar()
	
	select {
	case <-s.started:
		return nil // Already started
	default:
	}
	
	log.Infow("starting service", 
		"use_procedure_mode", s.cfg.UseProcedureMode,
		"working_directory", s.cfg.WorkingDirectory,
	)
	
	eg, egCtx := errgroup.WithContext(ctx)
	
	// Start core service components here
	// Each component will be added to the errgroup
	
	// Monitor for shutdown signal
	eg.Go(func() error {
		select {
		case <-s.shutdown:
			log.Infow("shutdown initiated")
			return nil
		case <-egCtx.Done():
			log.Infow("context cancelled")
			return egCtx.Err()
		}
	})
	
	// Signal that service has started
	close(s.started)
	
	// Block until all components finish or error
	err := eg.Wait()
	
	// Perform cleanup
	s.stop(ctx)
	
	return err
}

// Shutdown initiates graceful shutdown of the service (non-blocking)
func (s *Service) Shutdown(ctx context.Context) {
	log := s.logger.Sugar()
	log.Infow("shutdown requested")
	
	// Use atomic CAS to ensure only one shutdown
	if !s.shutdownStarted.CompareAndSwap(false, true) {
		// Already shutting down
		return
	}
	
	// We won the race, close the shutdown channel
	close(s.shutdown)
}

// stop performs the actual shutdown work
func (s *Service) stop(ctx context.Context) {
	log := s.logger.Sugar()
	log.Infow("stopping service")
	
	// Grace period for runners to finish
	if s.cfg.RunnerShutdownGracePeriod > 0 {
		graceCtx, cancel := context.WithTimeout(ctx, s.cfg.RunnerShutdownGracePeriod)
		defer cancel()
		
		// TODO: Signal runners to stop gracefully
		<-graceCtx.Done()
	}
	
	// Force stop all components
	// TODO: Stop all managed components
	
	// Signal that service has stopped
	select {
	case <-s.stopped:
		// Already stopped
	default:
		close(s.stopped)
	}
}

// IsStarted returns true if the service has been started
func (s *Service) IsStarted() bool {
	select {
	case <-s.started:
		return true
	default:
		return false
	}
}

// IsStopped returns true if the service has been stopped
func (s *Service) IsStopped() bool {
	select {
	case <-s.stopped:
		return true
	default:
		return false
	}
}

// IsRunning returns true if the service is running (started but not stopped)
func (s *Service) IsRunning() bool {
	return s.IsStarted() && !s.IsStopped()
}