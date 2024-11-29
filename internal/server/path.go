package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

func handleOutput(output interface{}) (interface{}, error) {
	if x, ok := output.(string); ok {
		return handlePath(x)
	} else if xs, ok := output.([]string); ok {
		for i, x := range xs {
			o, err := handlePath(x)
			if err != nil {
				return nil, err
			}
			xs[i] = o
		}
		return xs, nil
	} else if m, ok := output.(map[string]interface{}); ok {
		for key, value := range m {
			if s, ok := value.(string); ok {
				o, err := handlePath(s)
				if err != nil {
					return nil, err
				}
				m[key] = o
			}
		}
		return m, nil
	} else {
		return output, nil
	}
}

func handlePath(s string) (string, error) {
	p, ok := strings.CutPrefix(s, "file://")
	if !ok {
		return s, nil
	}

	mt := "application/octet-stream"
	if mime, err := mimetype.DetectFile(p); err == nil {
		mt = mime.Extension()
	}

	bs, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}

	b64 := base64.StdEncoding.EncodeToString(bs)
	return fmt.Sprintf("data:%s;base64,%s", mt, b64), nil
}
