package loggingtest

import (
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/replicate/cog-runtime/internal/logging"
)

// NewTestLogger creates a logger for tests that outputs to t.Logf
// Behaves exactly like zaptest.NewLogger but with trace support added
func NewTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	zapLogger := zaptest.NewLogger(t)
	return &logging.Logger{Logger: zapLogger}
}
