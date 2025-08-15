package tests

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestShutdownByServerSigInt(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog doesn't handle SIGINT properly without a TTY
		t.SkipNow()
	}
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	err := syscall.Kill(ct.ServerPid(), syscall.SIGINT)
	require.NoError(t, err)
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}

func TestShutdownByServerSigTerm(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	err := syscall.Kill(ct.ServerPid(), syscall.SIGTERM)
	require.NoError(t, err)
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}

func TestShutdownIgnoreSignal(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	// Ignore SIGTERM
	err := syscall.Kill(ct.ServerPid(), syscall.SIGTERM)
	require.NoError(t, err)
	assert.Nil(t, ct.cmd.ProcessState)
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	if *legacyCog {
		// Compat: legacy Cog doesn't handle SIGINT properly without a TTY
		ct.Shutdown()
	} else {
		// Handle SIGINT
		err := syscall.Kill(ct.ServerPid(), syscall.SIGINT)
		require.NoError(t, err)
	}
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}

func TestShutdownProcedureIgnoreSignal(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}
	ct := NewCogProcedureTest(t)
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	err := syscall.Kill(ct.ServerPid(), syscall.SIGTERM)
	require.NoError(t, err)
	assert.Nil(t, ct.cmd.ProcessState)
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}
