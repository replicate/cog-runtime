package tests

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/replicate/go/must"
	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/util"
)

var proceduresPath = filepath.Join(basePath, "python", "tests", "procedures")

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
	assert.Contains(t, string(must.Get(os.ReadFile(fooPy))), "'predicting foo'")

	fooDst2, err := util.PrepareProcedureSourceURL(srcDir, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, fooDst, fooDst2)
}

func TestPrepareProcedureSourceURLRemote(t *testing.T) {
	tmpDir := t.TempDir()

	fooTar := filepath.Join(tmpDir, "foo.tar.gz")
	fooDir := filepath.Join(proceduresPath, "foo")
	must.Do(exec.Command("tar", "-czf", fooTar, "-C", fooDir, ".").Run())

	barTar := filepath.Join(tmpDir, "bar.tar.gz")
	barDir := filepath.Join(proceduresPath, "bar")
	must.Do(exec.Command("tar", "-czf", barTar, "-C", barDir, ".").Run())

	port := util.FindPort()
	s := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.FileServer(http.Dir(tmpDir)),
	}
	defer s.Shutdown(context.Background())
	go func() {
		s.ListenAndServe()
	}()

	fooURL := fmt.Sprintf("http://localhost:%d/foo.tar.gz", port)
	fooDst, err := util.PrepareProcedureSourceURL(fooURL, 0)
	assert.NoError(t, err)
	assert.DirExists(t, fooDst)
	assert.FileExists(t, filepath.Join(fooDst, "cog.yaml"))
	fooPy := filepath.Join(fooDst, "predict.py")
	assert.FileExists(t, fooPy)
	assert.Contains(t, string(must.Get(os.ReadFile(fooPy))), "'predicting foo'")

	barURL := fmt.Sprintf("http://localhost:%d/bar.tar.gz", port)
	barDst, err := util.PrepareProcedureSourceURL(barURL, 0)
	assert.NoError(t, err)
	assert.DirExists(t, barDst)
	assert.FileExists(t, filepath.Join(barDst, "cog.yaml"))
	barPy := filepath.Join(barDst, "predict.py")
	assert.FileExists(t, barPy)
	assert.Contains(t, string(must.Get(os.ReadFile(barPy))), "'predicting bar'")

	fooDst2, err := util.PrepareProcedureSourceURL(fooURL, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, fooDst2, fooDst)

	barDst2, err := util.PrepareProcedureSourceURL(barURL, 1)
	assert.NoError(t, err)
	assert.NotEqual(t, barDst2, barDst)
}
