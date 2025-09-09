package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap/zaptest"
)

func TestService_Lifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		cfg := Config{
			Host:                      "localhost",
			Port:                      5000,
			UseProcedureMode:          false,
			WorkingDirectory:          "/tmp",
			RunnerShutdownGracePeriod: 5 * time.Second,
		}

		svc := New(cfg, logger)

		// Service should not be started initially
		if svc.IsStarted() {
			t.Error("service should not be started initially")
		}
		if svc.IsRunning() {
			t.Error("service should not be running initially")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start service in background
		done := make(chan error, 1)
		go func() {
			done <- svc.Run(ctx)
		}()

		// Wait for service to start
		<-svc.started

		// Service should be started and running
		if !svc.IsStarted() {
			t.Error("service should be started")
		}
		if !svc.IsRunning() {
			t.Error("service should be running")
		}
		if svc.IsStopped() {
			t.Error("service should not be stopped")
		}

		// Shutdown service
		svc.Shutdown(ctx)

		// Wait for service to finish
		err := <-done
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected error from Run: %v", err)
		}

		// Service should be stopped
		if !svc.IsStopped() {
			t.Error("service should be stopped")
		}
		if svc.IsRunning() {
			t.Error("service should not be running after shutdown")
		}
	})
}

func TestService_MultipleShutdowns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		cfg := Config{
			Host:                      "localhost",
			Port:                      5000,
			WorkingDirectory:          "/tmp",
			RunnerShutdownGracePeriod: 1 * time.Second,
		}

		svc := New(cfg, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start service in background
		done := make(chan error, 1)
		go func() {
			done <- svc.Run(ctx)
		}()


		// Multiple concurrent shutdowns should be safe
		for range 10 {
			go svc.Shutdown(ctx)
		}

		// Wait for service to finish
		err := <-done
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected error from Run: %v", err)
		}

		// Service should be stopped
		if !svc.IsStopped() {
			t.Error("service should be stopped")
		}
	})
}

func TestService_ContextCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		cfg := Config{
			Host:             "localhost",
			Port:             5000,
			WorkingDirectory: "/tmp",
		}

		svc := New(cfg, logger)

		ctx, cancel := context.WithCancel(context.Background())

		// Start service in background
		done := make(chan error, 1)
		go func() {
			done <- svc.Run(ctx)
		}()

		// Cancel context instead of explicit shutdown
		cancel()

		// Wait for service to finish
		err := <-done
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}

		// Service should be stopped
		if !svc.IsStopped() {
			t.Error("service should be stopped")
		}
	})
}