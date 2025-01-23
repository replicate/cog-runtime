package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/replicate/go/must"

	"github.com/gabriel-vasile/mimetype"
)

var BASE64_REGEX = regexp.MustCompile(`^data:.*;base64,(?P<base64>.*)$`)

func handlePath(output any, paths *[]string, fn func(string, *[]string) (string, error)) (any, error) {
	if x, ok := output.(string); ok {
		return fn(x, paths)
	} else if xs, ok := output.([]any); ok {
		for i, x := range xs {
			if s, ok := x.(string); ok {
				o, err := fn(s, paths)
				if err != nil {
					return nil, err
				}
				xs[i] = o
			}
		}
		return xs, nil
	} else if m, ok := output.(map[string]any); ok {
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

	bs, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	*paths = append(*paths, p)

	mt := mimetype.Detect(bs)
	b64 := base64.StdEncoding.EncodeToString(bs)
	return fmt.Sprintf("data:%s;base64,%s", mt, b64), nil
}

func outputToUpload(uploadUrl string, predictionId string) func(s string, paths *[]string) (string, error) {
	return func(s string, paths *[]string) (string, error) {
		p, ok := strings.CutPrefix(s, "file://")
		if !ok {
			return s, nil
		}

		bs, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		*paths = append(*paths, p)
		filename := path.Base(p)
		url := fmt.Sprintf("%s%s", uploadUrl, filename)
		req := must.Get(http.NewRequest(http.MethodPut, url, bytes.NewReader(bs)))
		req.Header.Set("X-Prediction-ID", predictionId)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		} else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return "", fmt.Errorf("failed to upload file: status %s", resp.Status)
		}
		return resp.Header.Get("Location"), nil
	}
}
