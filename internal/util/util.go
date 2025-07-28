package util

import (
	"embed"
	_ "embed"
	"encoding/base32"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/replicate/go/httpclient"

	"github.com/replicate/go/logging"

	"github.com/replicate/go/must"
	"github.com/replicate/go/uuid"

	"gopkg.in/yaml.v3"
)

var logger = logging.New("cog-util")

type Build struct {
	GPU        bool `yaml:"gpu"`
	Fast       bool `yaml:"fast"`
	CogRuntime bool `yaml:"cog_runtime"`
}

type Concurrency struct {
	Max int `yaml:"max"`
}

type CogYaml struct {
	Build       Build       `yaml:"build"`
	Concurrency Concurrency `yaml:"concurrency"`
	Predict     string      `yaml:"predict"`
}

func ReadCogYaml(dir string) (*CogYaml, error) {
	var cogYaml CogYaml
	bs, err := os.ReadFile(filepath.Join(dir, "cog.yaml"))
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(bs, &cogYaml); err != nil {
		return nil, err
	}
	return &cogYaml, nil
}

func (y *CogYaml) PredictModuleAndPredictor() (string, string, error) {
	parts := strings.Split(y.Predict, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid predict: %s", y.Predict)
	}
	moduleName := strings.TrimSuffix(parts[0], ".py")
	predictorName := parts[1]
	return moduleName, predictorName, nil
}

// api.git: internal/logic/id.go
func PredictionId() string {
	u := must.Get(uuid.NewV7())
	shuffle := make([]byte, uuid.Size)
	for i := 0; i < 4; i++ {
		shuffle[i], shuffle[i+4], shuffle[i+8], shuffle[i+12] = u[i+12], u[i+4], u[i], u[i+8]
	}
	encoding := base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)
	return encoding.EncodeToString(shuffle)
}

const TimeLayout = "2006-01-02T15:04:05.999999-07:00"

func NowIso() string {
	// Python: datetime.now(tz=timezone.utc).isoformat()
	return time.Now().UTC().Format(TimeLayout)
}

func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

func ParseTime(t string) time.Time {
	return must.Get(time.Parse(TimeLayout, t))
}

func JoinLogs(logs []string) string {
	r := strings.Join(logs, "\n")
	if r != "" {
		r += "\n"
	}
	return r
}

// Wildcard match in case version.txt is not generated yet
//
//go:embed *
var embedFS embed.FS

func Version() string {
	bs, err := embedFS.ReadFile("version.txt")
	if err != nil {
		return "0.0.0+unknown"
	}
	return strings.TrimSpace(string(bs))
}

func HTTPClientWithRetry() *http.Client {
	return httpclient.ApplyRetryPolicy(http.DefaultClient)
}
