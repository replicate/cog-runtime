package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/gabriel-vasile/mimetype"
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/replicate/cog-runtime/internal/util"
)

var Base64Regex = regexp.MustCompile(`^data:.*;base64,(?P<base64>.*)$`)

func isUri(s *openapi3.SchemaRef) bool {
	return s.Value.Type.Is("string") && s.Value.Format == "uri"
}

func handleInputPaths(input any, doc *openapi3.T, paths *[]string, fn func(string, *[]string) (string, error)) (any, error) {
	if doc == nil {
		return input, nil
	}

	schema, ok := doc.Components.Schemas["Input"]
	if !ok {
		return input, nil
	}

	// Input is always a `dict[str, Any]`
	m, ok := input.(map[string]any)
	if !ok {
		return input, nil
	}

	for k, v := range m {
		p, ok := schema.Value.Properties[k]
		if !ok {
			continue
		}
		if isUri(p) {
			// field: Path or field: Optional[Path]
			if s, ok := v.(string); ok {
				o, err := fn(s, paths)
				if err != nil {
					return nil, err
				}
				m[k] = o
			}
		} else if p.Value.Type.Is("array") && isUri(p.Value.Items) {
			// field: list[Path]
			if xs, ok := v.([]any); ok {
				for i, x := range xs {
					if s, ok := x.(string); ok {
						o, err := fn(s, paths)
						if err != nil {
							return nil, err
						}
						xs[i] = o
					}
				}
			}
		} else if p.Value.Type.Is("object") {
			// field is Any with custom coder, e.g. dataclass, JSON, or Pydantic
			// No known schema, try to handle all attributes
			o, err := handlePath(v, paths, fn)
			if err != nil {
				return nil, err
			}
			m[k] = o
		}
	}
	return input, nil
}

func handlePath(json any, paths *[]string, fn func(string, *[]string) (string, error)) (any, error) {
	if x, ok := json.(string); ok {
		return fn(x, paths)
	} else if xs, ok := json.([]any); ok {
		for i, x := range xs {
			if s, ok := x.(string); ok {
				o, err := fn(s, paths)
				if err != nil {
					return nil, err
				}
				xs[i] = o
			} else {
				o, err := handlePath(xs[i], paths, fn)
				if err != nil {
					return nil, err
				}
				xs[i] = o
			}
		}
		return xs, nil
	} else if m, ok := json.(map[string]any); ok {
		for key, value := range m {
			if s, ok := value.(string); ok {
				o, err := fn(s, paths)
				if err != nil {
					return nil, err
				}
				m[key] = o
			} else {
				o, err := handlePath(m[key], paths, fn)
				if err != nil {
					return nil, err
				}
				m[key] = o
			}
		}
		return m, nil
	} else {
		return json, nil
	}
}

func base64ToInput(s string, paths *[]string) (string, error) {
	m := Base64Regex.FindStringSubmatch(s)
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
	os.Chmod(f.Name(), 0o666)
	return f.Name(), nil
}

func urlToInput(s string, paths *[]string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return s, nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return s, nil
	}
	f, err := os.CreateTemp("", fmt.Sprintf("cog-input-*%s", filepath.Ext(u.Path)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	resp, err := util.HTTPClientWithRetry().Get(s)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	*paths = append(*paths, f.Name())
	os.Chmod(f.Name(), 0o666)
	return f.Name(), nil
}

func outputToBase64(s string, paths *[]string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return s, nil
	}
	if u.Scheme != "file" {
		return s, nil
	}
	p := u.Path

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
		u, err := url.Parse(s)
		if err != nil {
			return s, nil
		}
		if u.Scheme != "file" {
			return s, nil
		}
		p := u.Path

		bs, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		*paths = append(*paths, p)
		filename := path.Base(p)
		uUpload, err := url.JoinPath(uploadUrl, filename)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequest(http.MethodPut, uUpload, bytes.NewReader(bs))
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Prediction-ID", predictionId)
		req.Header.Set("Content-Type", mimetype.Detect(bs).String())
		resp, err := util.HTTPClientWithRetry().Do(req)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
			return "", fmt.Errorf("failed to upload file: status %s", resp.Status)
		}
		location := resp.Header.Get("Location")
		if location == "" {
			// In case upload server does not respond with Location
			location = uUpload
		}
		return location, nil
	}
}
