package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/replicate/cog-runtime/internal/config"
	"github.com/replicate/cog-runtime/internal/server"
)

// Server interface that both http.Server and httptest.Server can implement
type Server interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
	Close() error
}

// Service is the root lifecycle owner for the cog runtime
type Service struct {
	cfg config.Config

	// Lifecycle state
	started         chan struct{}
	stopped         chan struct{}
	shutdown        chan struct{}
	shutdownStarted atomic.Bool

	httpServer Server
	handler    *server.Handler

	logger *zap.Logger
}

// New creates a new Service with the given configuration
func New(cfg config.Config, baseLogger *zap.Logger) *Service {
	return &Service{
		cfg:      cfg,
		started:  make(chan struct{}),
		stopped:  make(chan struct{}),
		shutdown: make(chan struct{}),
		logger:   baseLogger.Named("service"),
	}
}

// Initialize sets up the service components (idempotent)
func (s *Service) Initialize() error {
	if err := s.initializeHTTPServer(); err != nil {
		return err
	}

	// TODO: Initialize other components (runners, webhooks, etc.)

	return nil
}

// initializeHTTPServer sets up the HTTP server if not already set
func (s *Service) initializeHTTPServer() error {
	if s.httpServer != nil {
		return nil // Already initialized with custom server (e.g., for tests)
	}

	log := s.logger.Sugar()
	log.Infow("initializing HTTP server")

	// Create the existing server handler (temporary until we refactor)
	forceShutdown := make(chan struct{}, 1)
	serverCfg := server.Config{
		UseProcedureMode:          s.cfg.UseProcedureMode,
		AwaitExplicitShutdown:     s.cfg.AwaitExplicitShutdown,
		OneShot:                   s.cfg.OneShot,
		IPCUrl:                    s.cfg.IPCUrl,
		UploadURL:                 s.cfg.UploadURL,
		WorkingDirectory:          s.cfg.WorkingDirectory,
		RunnerShutdownGracePeriod: s.cfg.RunnerShutdownGracePeriod,
		CleanupTimeout:            s.cfg.CleanupTimeout,
		ForceShutdown:             forceShutdown,
		MaxRunners:                s.cfg.MaxRunners,
	}

	tempCancel := func() {
		select {
		case <-s.shutdown:
		default:
			close(s.shutdown)
		}
	}

	h, err := server.NewHandler(serverCfg, tempCancel)
	if err != nil {
		return fmt.Errorf("failed to create server handler: %w", err)
	}

	// Store handler for proper shutdown
	s.handler = h

	// Create HTTP server with handler
	mux := server.NewServeMux(h, s.cfg.UseProcedureMode)
	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return nil
}

// Run starts the service and blocks until shutdown
func (s *Service) Run(ctx context.Context) error {
	log := s.logger.Sugar()

	select {
	case <-s.started:
		return nil // Already started
	default:
	}

	if s.httpServer == nil {
		return fmt.Errorf("service not initialized - call Initialize() first")
	}

	log.Infow("starting service",
		"use_procedure_mode", s.cfg.UseProcedureMode,
		"working_directory", s.cfg.WorkingDirectory,
	)

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		log.Infow("starting HTTP server")
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server failed: %w", err)
		}
		return nil
	})

	eg.Go(func() error {
		<-s.shutdown
		log.Infow("initiating graceful shutdown")

		// Signal runners to shutdown gracefully and wait for them
		if s.handler != nil {
			log.Infow("stopping runners gracefully")
			if err := s.handler.Stop(); err != nil {
				log.Errorw("error stopping handler", "error", err)
			}
		}

		// Hard shutdown HTTP server - use Close() for immediate shutdown
		log.Infow("closing HTTP server")
		return s.httpServer.Close()
	})

	// Monitor for context cancellation (handles external cancellation)
	eg.Go(func() error {
		select {
		case <-s.shutdown:
			// Shutdown was called, let shutdown handler deal with it
			return nil
		case <-egCtx.Done():
			log.Infow("context canceled, forcing immediate shutdown")
			// Signal shutdown first to unblock the shutdown handler
			if s.shutdownStarted.CompareAndSwap(false, true) {
				close(s.shutdown)
			}
			// Context canceled = immediate hard shutdown, no grace period
			if err := s.httpServer.Close(); err != nil {
				log.Errorw("failed to close HTTP server", "error", err)
			}
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

// stop performs final cleanup after shutdown
func (s *Service) stop(ctx context.Context) {
	log := s.logger.Sugar()
	log.Infow("stopping service")

	// Force stop any remaining components
	// TODO: Stop all managed components that weren't gracefully shut down

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
