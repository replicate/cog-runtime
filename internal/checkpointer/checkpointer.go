// There are some commands in here that are susceptible to injection. However, cog
// is a vehicle to let people run their own code... so why go through the hassle of
// injection? Cog is not run with any more permissions than the user code.
//
//nolint:gosec // See above
package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Configuration environment variables
	locationEnvVar          = "R8_LOCATION"
	shouldCheckpointEnvVar  = "R8_CUDA_CHECKPOINT"
	leaseFileEnvVar         = "R8_LEASE_FILE"
	cudaCheckpointDirEnvVar = "R8_CUDA_CHECKPOINT_DIR"
	cudaReadyFileEnvVar     = "R8_CUDA_READY_LOCK_FILE"

	// Dependencies for the checkpoint process
	cudaCheckpointURLFmtStr = "https://r8-public-assets-%s.cwobject.com/cuda-checkpoint"
	criuURLFmtStr           = "https://r8-public-assets-%s.cwobject.com/criu.tar.gz"
	cudaCheckpointPath      = "/tmp/cuda-checkpoint"
	criuPath                = "/tmp/criu"

	// Metadata storage paths
	cudaCmdFileName      = "cuda-cmd"
	checkpointSubdirName = "checkpoint"
)

var errNoCheckpointDir = errors.New("could not find checkpoint directory environment variable")

type FatalCheckpointError struct {
	err error
}

func (e *FatalCheckpointError) Error() string {
	return e.err.Error()
}

type Checkpointer interface {
	Disable()
	HasCheckpoint() bool
	Prepare(ctx context.Context) error
	Checkpoint(ctx context.Context, cmd *exec.Cmd, waitFunc func() error) error
	Restore(ctx context.Context) (*exec.Cmd, func(context.Context) error, error)
	WriteReadyFile() error
}

type checkpointer struct {
	enabled       bool
	hasCheckpoint bool
	checkpointDir string
	leaseFile     string
}

func NewCheckpointer(ctx context.Context) Checkpointer {
	return &checkpointer{
		enabled:       os.Getenv(shouldCheckpointEnvVar) == "true",
		checkpointDir: os.Getenv(cudaCheckpointDirEnvVar),
		leaseFile:     os.Getenv(leaseFileEnvVar),
	}
}

func (c *checkpointer) Disable() {
	c.enabled = false
}

func (c *checkpointer) HasCheckpoint() bool {
	if !c.enabled {
		return false
	}

	return c.hasCheckpoint
}

func (c *checkpointer) Prepare(ctx context.Context) error {
	if !c.enabled {
		return nil
	}

	// Download dependencies
	err := downloadCUDACheckpointBinaries(ctx)
	if err != nil {
		return err
	}

	// Wait for IPC lease file to be deleted
	if c.leaseFile != "" {
		err = pollForFileDeletion(c.leaseFile, 5*time.Minute, 10*time.Second)
		if err != nil {
			return err
		}
	}

	empty, err := isDirEmpty(filepath.Join(c.checkpointDir, checkpointSubdirName))
	// If the err is not nil, it probably means the directory does not exist
	if err == nil && !empty {
		c.hasCheckpoint = true
	}

	return nil
}

func (c *checkpointer) Checkpoint(ctx context.Context, cogletCmd *exec.Cmd, waitFunc func() error) error {
	if !c.enabled {
		return nil
	}

	if c.checkpointDir == "" {
		return errNoCheckpointDir
	}

	if err := waitFunc(); err != nil {
		return err
	}

	err := os.MkdirAll(filepath.Join(c.checkpointDir, checkpointSubdirName), 0o666)
	if err != nil {
		return err
	}

	pid := strconv.Itoa(cogletCmd.Process.Pid)

	// Find the PID of the command that is actually using the GPU
	cudaPIDBytes, err := exec.CommandContext(ctx, "nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader").Output()
	if err != nil {
		return err
	}

	cudaPID := strings.TrimSpace(string(cudaPIDBytes))

	// Get the command for this PID - it is _not_ always the root python process
	data, err := exec.CommandContext(ctx, "ps", "-o", "cmd=", cudaPID).Output()
	if err != nil {
		return err
	}

	cudaCmd := strings.TrimSpace(string(data))

	// Write said command to a file for later
	err = os.WriteFile(filepath.Join(c.checkpointDir, cudaCmdFileName), []byte(cudaCmd), 0o644)
	if err != nil {
		return err
	}

	// Toggle CUDA off
	cmd := exec.CommandContext(ctx, cudaCheckpointPath, "--toggle", "--pid", cudaPID)
	if err := cmd.Run(); err != nil {
		return err
	}

	// CRIU checkpoint (leaving process running)
	cmd = exec.CommandContext(ctx, criuPath, "dump", "--leave-running", "--shell-job", "--images-dir", filepath.Join(c.checkpointDir, checkpointSubdirName), "--tree", pid)
	if err := cmd.Run(); err != nil {
		// Try to toggle CUDA back on. If we aren't able to restart CUDA, the process
		// will hang indefinitely, so we should kill it and try to start a new one
		// without checkpointing
		cmd = exec.CommandContext(ctx, cudaCheckpointPath, "--toggle", "--pid", cudaPID)
		if cudaErr := cmd.Run(); cudaErr != nil {
			// Return a fatal error so upstream knows we cannot continue in the current state
			return &FatalCheckpointError{
				err: cudaErr,
			}
		}
		// Return the original checkpointing error
		return err
	}

	// Toggle CUDA back on. If we aren't able to restart CUDA, the process
	// will hang indefinitely, so we should kill it and try to start a new
	// one without checkpointing
	cmd = exec.CommandContext(ctx, cudaCheckpointPath, "--toggle", "--pid", cudaPID)
	if err := cmd.Run(); err != nil {
		// Return a fatal error so upstream knows we cannot continue in the current state
		return &FatalCheckpointError{
			err: err,
		}
	}

	return nil
}

func (c *checkpointer) Restore(ctx context.Context) (*exec.Cmd, func(context.Context) error, error) {
	if !c.enabled {
		return nil, nil, nil
	}

	// Read process from sentinel file
	cudaCmd, err := os.ReadFile(filepath.Join(c.checkpointDir, cudaCmdFileName))
	if err != nil {
		return nil, nil, err
	}

	// Set up restore command
	restoreCmd := exec.CommandContext(ctx, criuPath, "restore", "--shell-job", "--images-dir", filepath.Join(c.checkpointDir, checkpointSubdirName))

	// Set up callback function once restore is started
	callback := func(con context.Context) error {
		// Get the PID for the command
		cudaPID, err := exec.CommandContext(con, "pgrep", "-fx", string(cudaCmd)).Output()
		if err != nil {
			// If this command failed, we want to best effort try to kill the started process,
			// since we'll start a new one
			restoreCmd.Process.Kill() //nolint:errcheck // This is just best effort

			return err
		}

		// Toggle CUDA on for the restored process
		cmd := exec.CommandContext(con, cudaCheckpointPath, "--toggle", "--pid", string(cudaPID))
		if err := cmd.Run(); err != nil {
			// If this command failed, we want to best effort try to kill the started process,
			// since we'll start a new one
			restoreCmd.Process.Kill() //nolint:errcheck // This is just best effort

			return err
		}

		return nil
	}

	// The restored command is a running instance of coglet
	return restoreCmd, callback, nil
}

func (c *checkpointer) WriteReadyFile() error {
	// If it isn't expected, make this a no-op
	if os.Getenv(shouldCheckpointEnvVar) != "true" {
		return nil
	}
	return writeCudaReadyFile()
}

func downloadCUDACheckpointBinaries(ctx context.Context) error {
	location := os.Getenv("R8_LOCATION")

	// Download the cuda-checkpoint binary
	err := downloadAndChmod(fmt.Sprintf(cudaCheckpointURLFmtStr, location), cudaCheckpointPath)
	if err != nil {
		return fmt.Errorf("failed to download and chmod cuda-checkpoint binary: %w", err)
	}
	// CRIU gets downloaded as a tar with its dependencies. So we need to extract the tar, then
	// link the LD_LIBRARY_PATH to the dependencies
	dir := filepath.Dir(criuPath)
	err = downloadAndUntar(ctx, fmt.Sprintf(criuURLFmtStr, location), dir)
	if err != nil {
		return fmt.Errorf("failed to download and untar CRIU: %w", err)
	}
	return updateEnvVar("LD_LIBRARY_PATH", filepath.Join(dir, "criu-lib"))
}
