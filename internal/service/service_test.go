package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/replicate/cog-runtime/internal/config"
)

// testServer is a simple blocking server for tests that doesn't actually start HTTP
type testServer struct {
	shutdown chan struct{}
	closed   bool
}

func newTestServer() *testServer {
	return &testServer{
		shutdown: make(chan struct{}),
	}
}

func (t *testServer) ListenAndServe() error {
	// Just block until Close() is called
	<-t.shutdown
	return nil
}

func (t *testServer) Shutdown(ctx context.Context) error {
	return t.Close()
}

func (t *testServer) Close() error {
	if !t.closed {
		t.closed = true
		close(t.shutdown)
	}
	return nil
}

func TestService_Lifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		cfg := config.Config{
			Host:                      "localhost",
			Port:                      5000,
			UseProcedureMode:          false,
			WorkingDirectory:          "/tmp",
			RunnerShutdownGracePeriod: 10 * time.Millisecond, // Short for tests
		}

		svc := New(cfg, logger)
		// Set test server before Initialize() to avoid runner creation
		svc.httpServer = newTestServer()

		// Service should not be started initially
		assert.False(t, svc.IsStarted(), "service should not be started initially")
		assert.False(t, svc.IsRunning(), "service should not be running initially")

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
		assert.True(t, svc.IsStarted(), "service should be started")
		assert.True(t, svc.IsRunning(), "service should be running")
		assert.False(t, svc.IsStopped(), "service should not be stopped")

		// Shutdown service
		svc.Shutdown(ctx)

		// Wait for service to finish
		err := <-done
		require.NoError(t, err)

		// Service should be stopped
		assert.True(t, svc.IsStopped(), "service should be stopped")
		assert.False(t, svc.IsRunning(), "service should not be running after shutdown")
	})
}

func TestService_MultipleShutdowns(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)
	cfg := config.Config{
		Host:                      "localhost",
		Port:                      5000,
		WorkingDirectory:          "/tmp",
		RunnerShutdownGracePeriod: 1 * time.Second,
	}

	svc := New(cfg, logger)
	// Set test server before Initialize() to avoid runner creation
	svc.httpServer = newTestServer()

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
	require.NoError(t, err)

	// Service should be stopped
	assert.True(t, svc.IsStopped(), "service should be stopped")
}

func TestService_ContextCancellation(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)
	cfg := config.Config{
		Host:             "localhost",
		Port:             5000,
		WorkingDirectory: "/tmp",
	}

	svc := New(cfg, logger)
	// Set test server before Initialize() to avoid runner creation
	svc.httpServer = newTestServer()

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
	require.ErrorIs(t, err, context.Canceled)

	// Service should be stopped
	assert.True(t, svc.IsStopped(), "service should be stopped")
}
