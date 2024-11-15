package util

import (
	"encoding/base32"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/replicate/go/must"
	"github.com/replicate/go/uuid"

	"gopkg.in/yaml.v3"
)

type CogYaml struct {
	Predict string `yaml:"predict"`
}

func PredictFromCogYaml() (string, string, error) {
	var cogYaml CogYaml
	bs, err := os.ReadFile("cog.yaml")
	if err != nil {
		return "", "", err
	}
	if err := yaml.Unmarshal(bs, &cogYaml); err != nil {
		return "", "", err
	}
	parts := strings.Split(cogYaml.Predict, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid predict: %s", cogYaml.Predict)
	}
	moduleName := strings.TrimSuffix(parts[0], ".py")
	className := parts[1]
	return moduleName, className, nil
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

func NowIso() string {
	// Python: datetime.now(tz=timezone.utc).isoformat()
	return time.Now().UTC().Format("2006-01-02T15:04:05.999999-07:00")
}
