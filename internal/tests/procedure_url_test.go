package tests

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/util"
)

func TestPrepareProcedureSourceURLLocal(t *testing.T) {
	badDir, err := util.PrepareProcedureSourceURL("file:///foo/bar", 0)
	assert.ErrorContains(t, err, "no such file or directory")
	assert.Empty(t, badDir)

	fooDir := filepath.Join(proceduresPath, "foo")
	srcDir := fmt.Sprintf("file://%s", fooDir)
	fooDst, err := util.PrepareProcedureSourceURL(srcDir, 0)
	assert.NoError(t, err)
	assert.DirExists(t, fooDst)
	assert.FileExists(t, filepath.Join(fooDst, "cog.yaml"))
	fooPy := filepath.Join(fooDst, "predict.py")
	assert.FileExists(t, fooPy)
	fooPyContents, err := os.ReadFile(fooPy)
	require.NoError(t, err)
	assert.Contains(t, string(fooPyContents), "'predicting foo'")

	fooDst2, err := util.PrepareProcedureSourceURL(srcDir, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, fooDst, fooDst2)
}

// createMemTarGzFile creates a tarball in memory from the given directory and returns the []byte of the tarball
// so that it can then be served from an http.FileServer for test fixture reasons.
func createMemTarGzFile(t *testing.T, root string) []byte {
	t.Helper()

	fi, err := os.Stat(root)
	require.NoError(t, err)
	require.True(t, fi.IsDir())

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil // skip non-regulars; add handling if you want links, etc.
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel) // portable path in tar
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	require.NoError(t, err)

	twCloseErr := tw.Close()
	gzCloseErr := gz.Close()
	require.NoError(t, twCloseErr)
	require.NoError(t, gzCloseErr)

	return buf.Bytes()
}

func TestPrepareProcedureSourceURLRemote(t *testing.T) {
	t.Parallel()

	fooTar := createMemTarGzFile(t, filepath.Join(proceduresPath, "foo"))
	barTar := createMemTarGzFile(t, filepath.Join(proceduresPath, "bar"))

	testFS := fstest.MapFS{
		"foo.tar.gz": {
			Data: fooTar,
		},
		"bar.tar.gz": {
			Data: barTar,
		},
	}
	fileServer := httptest.NewServer(http.FileServerFS(testFS))
	t.Cleanup(fileServer.Close)

	fooURL := fileServer.URL + "/foo.tar.gz"
	fooDst, err := util.PrepareProcedureSourceURL(fooURL, 0)
	assert.NoError(t, err)
	assert.DirExists(t, fooDst)
	assert.FileExists(t, filepath.Join(fooDst, "cog.yaml"))
	fooPy := filepath.Join(fooDst, "predict.py")
	assert.FileExists(t, fooPy)
	fooPyContents, err := os.ReadFile(fooPy)
	require.NoError(t, err)
	assert.Contains(t, string(fooPyContents), "'predicting foo'")

	barURL := fileServer.URL + "/bar.tar.gz"
	barDst, err := util.PrepareProcedureSourceURL(barURL, 0)
	assert.NoError(t, err)
	assert.DirExists(t, barDst)
	assert.FileExists(t, filepath.Join(barDst, "cog.yaml"))
	barPy := filepath.Join(barDst, "predict.py")
	assert.FileExists(t, barPy)
	barPyContents, err := os.ReadFile(barPy)
	require.NoError(t, err)
	assert.Contains(t, string(barPyContents), "'predicting bar'")

	fooDst2, err := util.PrepareProcedureSourceURL(fooURL, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, fooDst2, fooDst)

	barDst2, err := util.PrepareProcedureSourceURL(barURL, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, barDst2, barDst)
}
