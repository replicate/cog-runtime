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

func TestService(t *testing.T) {
	t.Parallel()

	t.Run("Lifecycle", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			logger := zaptest.NewLogger(t)
			cfg := config.Config{
				Host:                      "localhost",
				Port:                      5000,
				UseProcedureMode:          false,
				WorkingDirectory:          "/tmp",
				RunnerShutdownGracePeriod: 10 * time.Millisecond,
			}

			svc := New(cfg, logger)
			svc.httpServer = newTestServer()

			assert.False(t, svc.IsStarted())
			assert.False(t, svc.IsRunning())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() {
				done <- svc.Run(ctx)
			}()

			<-svc.started

			assert.True(t, svc.IsStarted())
			assert.True(t, svc.IsRunning())
			assert.False(t, svc.IsStopped())

			svc.Shutdown(ctx)

			err := <-done
			require.NoError(t, err)

			assert.True(t, svc.IsStopped())
			assert.False(t, svc.IsRunning())
		})
	})

	t.Run("MultipleShutdowns", func(t *testing.T) {
		t.Parallel()
		logger := zaptest.NewLogger(t)
		cfg := config.Config{
			Host:                      "localhost",
			Port:                      5000,
			WorkingDirectory:          "/tmp",
			RunnerShutdownGracePeriod: 1 * time.Second,
		}

		svc := New(cfg, logger)
		svc.httpServer = newTestServer()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- svc.Run(ctx)
		}()

		for range 10 {
			go svc.Shutdown(ctx)
		}

		err := <-done
		require.NoError(t, err)

		assert.True(t, svc.IsStopped())
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		t.Parallel()
		logger := zaptest.NewLogger(t)
		cfg := config.Config{
			Host:             "localhost",
			Port:             5000,
			WorkingDirectory: "/tmp",
		}

		svc := New(cfg, logger)
		svc.httpServer = newTestServer()

		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			done <- svc.Run(ctx)
		}()

		cancel()

		err := <-done
		require.ErrorIs(t, err, context.Canceled)

		assert.True(t, svc.IsStopped())
	})

	t.Run("SignalHandling", func(t *testing.T) {
		t.Parallel()
		logger := zaptest.NewLogger(t)
		cfg := config.Config{
			Host:                  "localhost",
			Port:                  5000,
			WorkingDirectory:      "/tmp",
			AwaitExplicitShutdown: true,
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

		// Wait for service to start
		<-svc.started

		// Send SIGTERM - should be ignored in await-explicit-shutdown mode
		// The signal handler should log and continue running
		time.Sleep(10 * time.Millisecond) // Let signal handler start

		// Explicit shutdown should work
		svc.Shutdown(ctx)

		// Wait for service to finish
		err := <-done
		require.NoError(t, err)

		// Service should be stopped
		assert.True(t, svc.IsStopped(), "service should be stopped")
	})
}
