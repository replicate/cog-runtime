package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/replicate/cog-runtime/internal/config"
	"github.com/replicate/cog-runtime/internal/server"
)

// Service is the root lifecycle owner for the cog runtime
type Service struct {
	cfg config.Config

	// Lifecycle state
	started         chan struct{}
	stopped         chan struct{}
	shutdown        chan struct{}
	shutdownStarted atomic.Bool

	httpServer *http.Server
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
func (s *Service) Initialize(ctx context.Context) error {
	if err := s.initializeHTTPServer(ctx); err != nil {
		return err
	}

	return nil
}

// initializeHTTPServer sets up the HTTP server if not already set
func (s *Service) initializeHTTPServer(ctx context.Context) error {
	if s.httpServer != nil {
		return nil
	}

	log := s.logger.Sugar()
	log.Info("initializing HTTP server")

	forceShutdown := make(chan struct{}, 1)
	serverCfg := config.Config{
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

	h, err := server.NewHandler(ctx, serverCfg, tempCancel, s.logger) //nolint:contextcheck // context passing will come as we refactor
	if err != nil {
		return fmt.Errorf("failed to create server handler: %w", err)
	}

	s.handler = h

	mux := server.NewServeMux(h, s.cfg.UseProcedureMode)
	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(l net.Listener) context.Context { return ctx },
	}

	return nil
}

// Run starts the service and blocks until shutdown
func (s *Service) Run(ctx context.Context) error {
	log := s.logger.Sugar()

	select {
	case <-s.started:
		log.Errorw("service already started")
		return nil
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

	// Start handler (which starts its internal runner manager)
	if err := s.handler.Start(egCtx); err != nil {
		return fmt.Errorf("failed to start handler: %w", err)
	}

	eg.Go(func() error {
		log.Info("starting HTTP server")
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server failed: %w", err)
		}
		return nil
	})

	eg.Go(func() error {
		<-s.shutdown
		log.Info("initiating graceful shutdown")

		// Signal runners to shutdown gracefully and wait for them
		if s.handler != nil {
			log.Info("stopping runners gracefully")
			if err := s.handler.Stop(); err != nil {
				log.Errorw("error stopping handler", "error", err)
			}
		}

		log.Info("closing HTTP server")
		return s.httpServer.Close()
	})

	// Monitor for context cancellation (handles external cancellation)
	eg.Go(func() error {
		select {
		case <-s.shutdown:
			// Shutdown was called, let shutdown handler deal with it
			return nil
		case <-egCtx.Done():
			log.Info("context canceled, forcing immediate shutdown")
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

	// Handle OS signals only in await-explicit-shutdown mode
	if s.cfg.AwaitExplicitShutdown {
		eg.Go(func() error {
			return s.handleSignals(egCtx)
		})
	}

	close(s.started)

	err := eg.Wait()

	s.stop(ctx)

	return err
}

// Shutdown initiates graceful shutdown of the service (non-blocking)
func (s *Service) Shutdown(ctx context.Context) {
	log := s.logger.Sugar()
	log.Info("shutdown requested")

	// Use atomic CAS to ensure only one shutdown
	if !s.shutdownStarted.CompareAndSwap(false, true) {
		log.Debug("already shutting down")
		return
	}

	close(s.shutdown)
}

// stop performs final cleanup after shutdown
func (s *Service) stop(ctx context.Context) {
	log := s.logger.Sugar()
	log.Info("stopping service")

	select {
	case <-s.stopped:
		log.Debug("service already stopped")
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

// handleSignals handles SIGTERM in await-explicit-shutdown mode
func (s *Service) handleSignals(ctx context.Context) error {
	log := s.logger.Sugar()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)

	select {
	case <-s.shutdown:
		return nil
	case <-ctx.Done():
		return nil
	case <-ch:
		log.Info("received SIGTERM, starting graceful shutdown")
		s.Shutdown(ctx)
		return nil
	}
}
