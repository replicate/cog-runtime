package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/replicate/cog-runtime/internal/util"
	"github.com/replicate/cog-runtime/internal/webhook"
)

type Status int

const (
	StatusInvalid Status = iota - 1 // -1 is invalid status
	StatusStarting
	StatusSetupFailed
	StatusReady
	StatusBusy
	StatusDefunct
)

func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "STARTING"
	case StatusSetupFailed:
		return "SETUP_FAILED"
	case StatusReady:
		return "READY"
	case StatusBusy:
		return "BUSY"
	case StatusDefunct:
		return "DEFUNCT"
	default:
		return "INVALID"
	}
}

func StatusFromString(statusStr string) (Status, error) {
	switch statusStr {
	case "READY":
		return StatusReady, nil
	case "BUSY":
		return StatusBusy, nil
	case "STARTING":
		return StatusStarting, nil
	case "SETUP_FAILED":
		return StatusSetupFailed, nil
	case "DEFUNCT":
		return StatusDefunct, nil
	default:
		return StatusInvalid, fmt.Errorf("unknown status: %s", statusStr)
	}
}

type SetupStatus string

const (
	SetupSucceeded SetupStatus = "succeeded"
	SetupFailed    SetupStatus = "failed"
)

type Concurrency struct {
	Max     int `json:"max,omitempty"`
	Current int `json:"current,omitempty"`
}

type SetupResult struct {
	Status SetupStatus `json:"status"`
	Logs   string      `json:"logs,omitempty"`
	Schema string      `json:"schema,omitempty"`
}

type PredictionRequest struct {
	Input               any             `json:"input"`
	ID                  string          `json:"id"`
	CreatedAt           string          `json:"created_at"`
	StartedAt           string          `json:"started_at"`
	Webhook             string          `json:"webhook,omitempty"`
	WebhookEventsFilter []webhook.Event `json:"webhook_events_filter,omitempty"`
	OutputFilePrefix    string          `json:"output_file_prefix,omitempty"`
	Context             map[string]any  `json:"context"`

	ProcedureSourceURL string `json:"-"` // this is not sent to the python code, used internally
}

type PredictionStatus string

const (
	PredictionStarting   PredictionStatus = "starting"
	PredictionProcessing PredictionStatus = "processing"
	PredictionSucceeded  PredictionStatus = "succeeded"
	PredictionCanceled   PredictionStatus = "canceled"
	PredictionFailed     PredictionStatus = "failed"
)

func (s PredictionStatus) IsCompleted() bool {
	return s == PredictionSucceeded || s == PredictionCanceled || s == PredictionFailed
}

type PredictionResponse struct {
	ID         string           `json:"id"`
	Status     PredictionStatus `json:"status"`
	Input      any              `json:"input,omitempty"`
	Output     any              `json:"output,omitempty"`
	Error      string           `json:"error,omitempty"`
	Logs       []string         `json:"logs,omitempty"`
	Metrics    any              `json:"metrics,omitempty"`
	WebhookURL string           `json:"webhook,omitempty"`
}

// MarshalJSON implements custom JSON marshaling to convert logs from []string to string
func (pr PredictionResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID         string           `json:"id"`
		Status     PredictionStatus `json:"status"`
		Input      any              `json:"input,omitempty"`
		Output     any              `json:"output,omitempty"`
		Error      string           `json:"error,omitempty"`
		Logs       string           `json:"logs,omitempty"`
		Metrics    any              `json:"metrics,omitempty"`
		WebhookURL string           `json:"webhook,omitempty"`
	}{
		ID:         pr.ID,
		Status:     pr.Status,
		Input:      pr.Input,
		Output:     pr.Output,
		Error:      pr.Error,
		Logs:       util.JoinLogs(pr.Logs),
		Metrics:    pr.Metrics,
		WebhookURL: pr.WebhookURL,
	})
}

// UnmarshalJSON implements custom JSON unmarshalling to convert logs from string to []string
func (pr *PredictionResponse) UnmarshalJSON(data []byte) error {
	aux := &struct {
		ID         string           `json:"id"`
		Status     PredictionStatus `json:"status"`
		Input      any              `json:"input,omitempty"`
		Output     any              `json:"output,omitempty"`
		Error      string           `json:"error,omitempty"`
		Logs       string           `json:"logs,omitempty"`
		Metrics    any              `json:"metrics,omitempty"`
		WebhookURL string           `json:"webhook,omitempty"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	pr.ID = aux.ID
	pr.Status = aux.Status
	pr.Input = aux.Input
	pr.Output = aux.Output
	pr.Error = aux.Error
	pr.Metrics = aux.Metrics
	pr.WebhookURL = aux.WebhookURL

	// Convert string logs back to []string
	if aux.Logs != "" {
		// Split on newline and remove the trailing empty element if it exists
		parts := strings.Split(aux.Logs, "\n")
		if len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		pr.Logs = parts
	} else {
		pr.Logs = nil
	}
	return nil
}

// RunnerID is a unique identifier for a runner instance.
// Format: 8-character base32 string (no leading zeros)
// Example: "k7m3n8p2", "b9q4x2w1"
type RunnerID string

// GenerateRunnerID generates a new random runner ID
func GenerateRunnerID() RunnerID {
	// 5 buf = 40 bits = 8 base32 chars
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return RunnerID(fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff))
	}

	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	id := strings.ToLower(encoded[:8]) // Take first 8 chars

	// Replace leading zero with 'a'
	if strings.HasPrefix(id, "0") {
		id = "a" + id[1:]
	}

	return RunnerID(id)
}

func (r RunnerID) String() string {
	return string(r)
}

// RunnerContext contains everything a runner needs to operate
type RunnerContext struct {
	id                 string
	workingdir         string
	tmpDir             string
	uploader           *uploader
	uid                *int     // UID used for setUID isolation, nil if not using setUID
	cleanupDirectories []string // Directories to walk for cleanup of files owned by isolated UIDs
}

func (rc *RunnerContext) Cleanup() error {
	if rc.tmpDir != "" {
		if err := os.RemoveAll(rc.tmpDir); err != nil {
			return err
		}
	}

	// Clean up files in configured directories owned by this UID when using setUID isolation
	if rc.uid != nil && len(rc.cleanupDirectories) > 0 {
		return rc.cleanupDirectoriesFiles()
	}

	return nil
}

// cleanupDirectoriesFiles removes files in configured directories owned by the isolated UID
func (rc *RunnerContext) cleanupDirectoriesFiles() error {
	if rc.uid == nil {
		return nil
	}

	// Avoid cleaning our own workingdir/tmpdir if they're in the cleanup directories
	skipPaths := make(map[string]bool)
	for _, cleanupDir := range rc.cleanupDirectories {
		if strings.HasPrefix(rc.workingdir, cleanupDir+"/") {
			skipPaths[rc.workingdir] = true
		}
		if strings.HasPrefix(rc.tmpDir, cleanupDir+"/") {
			skipPaths[rc.tmpDir] = true
		}
	}

	for _, cleanupDir := range rc.cleanupDirectories {
		// Use os.OpenRoot to create a secure chrooted view of the cleanup directory
		root, err := os.OpenRoot(cleanupDir)
		if err != nil {
			continue // Skip directories we can't root into
		}

		err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // Continue walking on errors
			}

			// Convert relative path back to absolute for skipPaths check
			absPath := filepath.Join(cleanupDir, path)
			if skipPaths[absPath] {
				return filepath.SkipDir
			}

			// Don't follow symlinks
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}

			// Check if file is owned by our UID using root.Stat
			info, err := root.Stat(path)
			if err != nil {
				return nil // Continue on stat errors
			}

			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				if int(stat.Uid) == *rc.uid {
					if err := root.RemoveAll(path); err != nil {
						// Log error but continue cleanup
						return nil
					}
					if d.IsDir() {
						return filepath.SkipDir
					}
				}
			}

			return nil
		})

		// Close the root after processing this directory
		_ = root.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

type PendingPrediction struct {
	request     PredictionRequest
	response    PredictionResponse
	lastUpdated time.Time
	inputPaths  []string
	outputCache map[string]string
	mu          sync.Mutex
	c           chan PredictionResponse
	closed      bool

	// Per-prediction watcher cancellation and notification
	cancel       context.CancelFunc
	watcherDone  chan struct{}
	outputNotify chan struct{} // Receives OUTPUT IPC events for this prediction

	terminalWebhookSent atomic.Bool
	webhookSender       webhook.Sender
}

func (p *PendingPrediction) safeSend(resp PredictionResponse) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}
	select {
	case p.c <- resp:
		return true
	default:
		return false
	}
}

func (p *PendingPrediction) safeClose() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}
	p.closed = true
	close(p.c)
	return true
}

// sendWebhook sends a webhook asynchronously
func (p *PendingPrediction) sendWebhook(event webhook.Event) error {
	if p.request.Webhook == "" || p.webhookSender == nil {
		return nil
	}

	p.mu.Lock()
	body, err := json.Marshal(p.response)
	if err != nil {
		return fmt.Errorf("failed to marshal prediction response: %w", err)
	}
	p.mu.Unlock()

	// Use the prediction response as the webhook payload
	go func() {
		_ = p.webhookSender.SendConditional(p.request.Webhook, bytes.NewReader(body), event, p.request.WebhookEventsFilter, &p.lastUpdated)
	}()
	return nil
}

// sendWebhookSync sends a webhook synchronously
func (p *PendingPrediction) sendWebhookSync(event webhook.Event) error {
	if p.request.Webhook == "" || p.webhookSender == nil {
		return nil
	}

	p.mu.Lock()
	body, err := json.Marshal(p.response)
	if err != nil {
		return fmt.Errorf("failed to marshal prediction response: %w", err)
	}
	p.mu.Unlock()

	// Send webhook synchronously for terminal events
	_ = p.webhookSender.SendConditional(p.request.Webhook, bytes.NewReader(body), event, p.request.WebhookEventsFilter, &p.lastUpdated)
	return nil
}
