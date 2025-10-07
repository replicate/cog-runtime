package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var errTimedOutPolling = errors.New("Timed out while polling for file")

// updateEnvVar updates an environment variable in-place, adding an item to it
// if it exists or creating it if it doesn't exist
func updateEnvVar(envVarName, newItem string) {
	old := os.Getenv(envVarName)
	if old == "" {
		os.Setenv(envVarName, newItem)
		return
	}
	path := newItem + string(os.PathListSeparator) + os.Getenv(envVarName)
	os.Setenv(envVarName, path)
}

// downloadFile downloads a file from the URL provided to the path provided
func downloadFile(url, path string) error {
	filename := filepath.Base(path)
	os.MkdirAll(filepath.Dir(path), 0o755)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", filename, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: %w", filename, err)
	}

	binary, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to touch file: %w", err)
	}
	defer binary.Close()

	_, err = io.Copy(binary, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save %s: %w", filename, err)
	}

	return nil
}

// downloadAndChmod downloads a file to the path provided and chmods it for
// execution. This expects the downloaded file to be a binary
func downloadAndChmod(url, path string) error {
	err := downloadFile(url, path)
	if err != nil {
		return err
	}

	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("failed to chmod file: %w", err)
	}
	return nil
}

// downloadAndUntar downloads a tar and extracts it to a path. The path is expected
// to be a directory
func downloadAndUntar(ctx context.Context, url, path string) error {
	// Download to `${path}/tmp.tar.gz`
	downloadPath := filepath.Join(path, "tmp.tar.gz")
	err := downloadFile(url, downloadPath)
	if err != nil {
		return err
	}

	// Untar into the `${path}` dir
	cmd := exec.CommandContext(ctx, "tar", "-xf", downloadPath, "-C", path)
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	cmd.Stdout = devnull
	cmd.Stderr = devnull

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract tar: %w", err)
	}

	return nil
}

// pollForFileDeletion waits for a file to be deleted, up until a timeout. It returns an error if the
// timeout is hit
func pollForFileDeletion(target string, timeout, pollInterval time.Duration) error {
	deadline := time.After(timeout)

	for {
		// Check if the file still exists, if it does keep looping
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			return nil
		}

		// Check for timeout before sleeping for the polling interval
		select {
		case <-deadline:
			return errTimedOutPolling
		default:
			time.Sleep(pollInterval)
		}
	}
}

// pollForFileExistance waits for a file to exist, up until a timeout. It returns an error if the
// timeout is hit
func pollForFileExistance(target string, timeout, pollInterval time.Duration) error {
	deadline := time.After(timeout)

	for {
		// Check if the file exists, if it doesn't keep looping
		if _, err := os.Stat(target); err == nil {
			return nil
		}

		// Check for timeout before sleeping for the polling interval
		select {
		case <-deadline:
			return errTimedOutPolling
		default:
			time.Sleep(pollInterval)
		}
	}
}

// https://stackoverflow.com/a/30708914/30548878
func isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// Touch a file if it doesn't exist, otherwise wipes the contents of the file
func touchFile(name string) error {
	// Ensure upstream directory exists for file
	err := os.MkdirAll(filepath.Dir(name), 0o755)
	if err != nil {
		return err
	}

	f, err := os.Create(name)
	if err != nil {
		return err
	}
	f.Close()
	return nil
}

// setStatusReady ensures the ready files exist
func setStatusReady() error {
	cudaReadyFilePath := os.Getenv(cudaReadyFileEnvVar)

	// Touch CUDA ready file
	err := touchFile(cudaReadyFilePath)
	if err != nil {
		return err
	}

	return nil
}
