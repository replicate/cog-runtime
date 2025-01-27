package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/replicate/go/logging"
	"github.com/replicate/go/must"

	"github.com/replicate/cog-runtime/internal/util"
)

var logger = logging.New("cog-schema")

func main() {
	log := logger.Sugar()
	m, c, err := util.PredictFromCogYaml()
	if err != nil {
		log.Errorw("failed to parse cog.yaml", "err", err)
		return
	}
	bin := must.Get(exec.LookPath("python3"))
	fmt.Println(bin)
	fmt.Println(os.Environ())
	must.Do(syscall.Exec(bin, []string{bin, "-m", "coglet.schema", m, c}, os.Environ()))
}
