package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

func copyRecursiveSymlink(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dstRoot, relPath)

		if d.IsDir() {
			if path != srcRoot {
				return os.MkdirAll(dstPath, 0o755)
			}
			return nil
		}
		return os.Symlink(path, dstPath)
	})
}

func PrepareProcedureSourceURL(srcURL string) (string, error) {
	u, err := url.Parse(srcURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "file" {
		// file:///path/to/existing/dir
		stat, err := os.Stat(u.Path)
		if err != nil {
			return "", err
		}
		if !stat.IsDir() {
			return "", fmt.Errorf("invalid procedure source URL: %s", srcURL)
		}
		tmpDir, err := os.MkdirTemp("", "procedure*")
		if err != nil {
			return "", err
		}
		err = copyRecursiveSymlink(u.Path, tmpDir)
		if err != nil {
			return "", err
		}
		return tmpDir, nil
	} else if u.Scheme == "http" || u.Scheme == "https" {
		// http://host/path/to/tarball
		sha := sha256.New()
		sha.Write([]byte(srcURL))
		hash := hex.EncodeToString(sha.Sum(nil))
		dstDir := filepath.Join(os.TempDir(), fmt.Sprintf("procedure-%s", hash))

		// Already downloaded
		stat, err := os.Stat(dstDir)
		if err == nil && stat.IsDir() {
			return dstDir, nil
		}

		// Download to temporary file
		// tar -xf cannot detect compression from stdin and file should be small enough
		resp, err := http.Get(srcURL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		tarball, err := os.CreateTemp("", fmt.Sprintf("procedure-%s-*", hash))
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(tarball, resp.Body); err != nil {
			return "", err
		}
		defer os.Remove(tarball.Name())

		// Extract tarball
		if err := os.MkdirAll(dstDir, 0o700); err != nil {
			return "", err
		}
		cmd := exec.Command("tar", "-xf", tarball.Name(), "-C", dstDir)
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return dstDir, nil
	}
	return "", fmt.Errorf("invalid procedure source URL: %s", srcURL)
}
