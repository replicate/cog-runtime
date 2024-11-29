package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

var BASE64_REGEX = regexp.MustCompile(`^data:.*;base64,(?P<base64>.*)$`)

func handlePath(output interface{}, paths *[]string, fn func(string, *[]string) (string, error)) (interface{}, error) {
	if x, ok := output.(string); ok {
		return fn(x, paths)
	} else if xs, ok := output.([]string); ok {
		for i, x := range xs {
			o, err := fn(x, paths)
			if err != nil {
				return nil, err
			}
			xs[i] = o
		}
		return xs, nil
	} else if m, ok := output.(map[string]interface{}); ok {
		for key, value := range m {
			if s, ok := value.(string); ok {
				o, err := fn(s, paths)
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

func base64ToInput(s string, paths *[]string) (string, error) {
	m := BASE64_REGEX.FindStringSubmatch(s)
	if m == nil {
		return s, nil
	}
	bs, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "cog-input-")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(bs); err != nil {
		return "", err
	}
	*paths = append(*paths, f.Name())
	return f.Name(), nil
}

func outputToBase64(s string, paths *[]string) (string, error) {
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
	*paths = append(*paths, p)
	b64 := base64.StdEncoding.EncodeToString(bs)
	return fmt.Sprintf("data:%s;base64,%s", mt, b64), nil
}
