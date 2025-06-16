package tests

import (
	"syscall"
	"testing"

	"github.com/replicate/go/must"
	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestShutdownByServerSigTerm(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	must.Do(syscall.Kill(ct.ServerPid(), syscall.SIGTERM))
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}

func TestShutdownByRunnerSigTerm(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	must.Do(syscall.Kill(ct.ServerPid(), syscall.SIGTERM))
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

	must.Do(syscall.Kill(ct.ServerPid(), syscall.SIGTERM))
	assert.Nil(t, ct.cmd.ProcessState)
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}

func TestProcedureIgnoreSignal(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}
	ct := NewCogProcedureTest(t)
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	must.Do(syscall.Kill(ct.ServerPid(), syscall.SIGTERM))
	assert.Nil(t, ct.cmd.ProcessState)
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
	assert.Equal(t, 0, ct.cmd.ProcessState.ExitCode())
}
