package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/replicate/go/must"

	"github.com/replicate/cog-runtime/internal/util"
)

var LOG_REGEX = regexp.MustCompile(`^\[pid=(?P<pid>[^\\]+)] (?P<msg>.*)$`)
var RESPONSE_REGEX = regexp.MustCompile(`^response-(?P<pid>\S+).json$`)
var RESPONSE_FMT = "response-%s.json"

type PendingPrediction struct {
	request PredictionRequest
	logs    []string
	c       chan PredictionResponse
}

type Runner struct {
	workingDir            string
	cmd                   exec.Cmd
	status                Status
	schema                string
	setupResult           *SetupResult
	logs                  []string
	pending               map[string]PendingPrediction
	awaitExplicitShutdown bool
	shutdownRequested     bool

	mu sync.Mutex
}

func NewRunner(workingDir, moduleName, className string, awaitExplicitShutdown bool) *Runner {
	args := []string{
		"-u",
		"-m", "coglet",
		"--working-dir", workingDir,
		"--module-name", moduleName,
		"--class-name", className,
	}
	cmd := exec.Command("python3", args...)
	return &Runner{
		workingDir:            workingDir,
		cmd:                   *cmd,
		status:                StatusStarting,
		pending:               make(map[string]PendingPrediction),
		awaitExplicitShutdown: awaitExplicitShutdown,
	}
}

func (r *Runner) Start() error {
	log := logger.Sugar()
	cmdStart := make(chan bool)
	if err := r.setupLogging(cmdStart); err != nil {
		log.Errorw("failed to setup logging", "error", err)
		return err
	}
	// Placeholder in case setup crashes
	r.setupResult = &SetupResult{
		StartedAt: util.NowIso(),
	}
	if err := r.cmd.Start(); err != nil {
		log.Errorw("failed to start command", "error", err)
		return err
	}
	log.Infow("python runner started", "pid", r.cmd.Process.Pid)
	close(cmdStart)
	go r.wait()
	go r.handleSignals()
	return nil
}

func (r *Runner) Shutdown() error {
	log := logger.Sugar()
	log.Infow("shutdown requested")
	r.shutdownRequested = true
	if r.cmd.ProcessState != nil {
		// Python process already exited
		// Terminate HTTP server
		return r.stop()
	} else {
		// Otherwise signal Python process to stop
		// FIXME: kill process after grace period
		p := path.Join(r.workingDir, "stop")
		return os.WriteFile(p, []byte{}, 0644)
	}
}

func (r *Runner) ExitCode() int {
	return r.cmd.ProcessState.ExitCode()
}

func (r *Runner) stop() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}

////////////////////
// Prediction

func (r *Runner) predict(req PredictionRequest) (chan PredictionResponse, error) {
	log := logger.Sugar()
	if r.status == StatusSetupFailed {
		log.Errorw("prediction rejected: setup failed")
		return nil, fmt.Errorf("setup failed")
	} else if r.status == StatusDefunct {
		log.Errorw("prediction rejected: server is defunct")
		return nil, fmt.Errorf("server is defunct")
	}
	// We can still queue predictions if status is starting or busy
	if req.Id == "" {
		req.Id = util.PredictionId()
	}
	if req.CreatedAt == "" {
		req.CreatedAt = util.NowIso()
	}
	if _, ok := r.pending[req.Id]; ok {
		log.Errorw("prediction rejected: prediction ID exists", "id", req.Id)
		return nil, fmt.Errorf("prediction ID exists")
	}

	log.Infow("received prediction request", "id", req.Id)
	reqPath := path.Join(r.workingDir, fmt.Sprintf("request-%s.json", req.Id))
	must.Do(os.WriteFile(reqPath, must.Get(json.Marshal(req)), 0644))
	pr := PendingPrediction{request: req}
	if req.Webhook == "" {
		pr.c = make(chan PredictionResponse, 1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[req.Id] = pr
	return pr.c, nil
}

////////////////////
// Background tasks

func (r *Runner) wait() {
	log := logger.Sugar()
	err := r.cmd.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		logs := r.rotateLogs()
		log.Errorw("python runner excited with error", "pid", r.cmd.Process.Pid, "error", err, "logs", logs)
		if r.status == StatusStarting {
			r.status = StatusSetupFailed
			r.setupResult.CompletedAt = util.NowIso()
			r.setupResult.Status = "failed"
			r.setupResult.Logs = logs
		} else {
			r.status = StatusDefunct
		}
	} else {
		log.Infow("python runner exited successfully", "pid", r.cmd.Process.Pid)
		r.status = StatusDefunct
	}
	if !r.awaitExplicitShutdown || r.shutdownRequested {
		must.Do(r.stop())
	}
}

func (r *Runner) handleSignals() {
	log := logger.Sugar()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, SigOutput, SigReady, SigBusy)
	for {
		s := <-ch
		if s == SigOutput {
			r.handleResponses()
		} else if s == SigReady {
			if r.status == StatusStarting {
				r.updateSchema()
				r.updateSetupResult()
			}
			log.Info("runner is ready")
			r.status = StatusReady
		} else if s == SigBusy {
			log.Info("runner is busy")
			r.status = StatusBusy
		}
	}
}

////////////////////
// IO handling

func (r *Runner) updateSchema() {
	log := logger.Sugar()
	log.Infow("updating OpenAPI schema")
	p := path.Join(r.workingDir, "openapi.json")
	schema := string(must.Get(os.ReadFile(p)))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schema = schema
}

func (r *Runner) updateSetupResult() {
	log := logger.Sugar()
	log.Infow("updating setup result")
	var setupResult SetupResult
	must.Do(r.readJson("setup_result.json", &setupResult))
	if setupResult.Status == "succeeded" {
		log.Infow("setup succeeded")
		r.status = StatusReady
	} else if setupResult.Status == "failed" {
		log.Errorw("setup failed")
		r.status = StatusSetupFailed
	} else {
		panic(fmt.Sprintf("invalid setup status: %s", r.setupResult.Status))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	setupResult.Logs = r.rotateLogs()
	r.setupResult = &setupResult
}

func (r *Runner) handleResponses() {
	log := logger.Sugar()
	completed := make(map[string]bool)
	for _, entry := range must.Get(os.ReadDir(r.workingDir)) {
		m := RESPONSE_REGEX.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		pid := m[1]
		pr, ok := r.pending[pid]
		if !ok {
			continue
		}
		log.Infow("received prediction response", "id", pr.request.Id)
		var resp PredictionResponse
		must.Do(r.readJson(entry.Name(), &resp))

		// Copy request fields because the Python FileRunner does not
		resp.Id = pr.request.Id
		resp.Input = pr.request.Input
		resp.CreatedAt = pr.request.CreatedAt

		// FIXME: handle async predictions
		r.mu.Lock()
		resp.Logs = strings.Join(r.logs, "\n") + "\n"
		r.mu.Unlock()

		// FIXME: webhook interval
		if pr.request.Webhook != "" {
			webhook(pr.request.Webhook, resp)
		}
		fmt.Println(resp)

		if _, ok := PredictionCompletedStatuses[resp.Status]; ok {
			completed[pid] = true
			log.Infow("prediction completed", "id", pr.request.Id, "status", resp.Status)
			if pr.c != nil {
				pr.c <- resp
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for pid, _ := range completed {
		delete(r.pending, pid)
		must.Do(os.Remove(path.Join(r.workingDir, fmt.Sprintf(RESPONSE_FMT, pid))))
	}
}

func (r *Runner) readJson(filename string, v any) error {
	log := logger.Sugar()
	p := path.Join(r.workingDir, filename)
	bs, err := os.ReadFile(p)
	if err != nil {
		log.Errorw("failed to read JSON file", "filename", filename, "error", err)
		return err
	}
	return json.Unmarshal(bs, v)
}

func webhook(url string, response PredictionResponse) {
	log := logger.Sugar()
	body := bytes.NewBuffer(must.Get(json.Marshal(response)))
	req := must.Get(http.NewRequest("POST", url, body))
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Errorw("failed to send webhook", "error", err)
	} else if resp.StatusCode != 200 {
		body := string(must.Get(io.ReadAll(resp.Body)))
		log.Errorw("failed to send webhook", "code", resp.StatusCode, "body", body)
	}
}

////////////////////
// Log handling

func (r *Runner) log(line string) {
	log := logger.Sugar()
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := LOG_REGEX.FindStringSubmatch(line); m != nil {
		pid := m[1]
		msg := m[2]
		if pr, ok := r.pending[pid]; ok {
			pr.logs = append(pr.logs, msg)
		} else {
			log.Errorw("received log for non-existent prediction", "id", pid, "message", msg)
		}
	} else if !strings.Contains(line, "[cog-file-runner]") {
		r.logs = append(r.logs, line)
	}
	fmt.Println(line)
}

func (r *Runner) rotateLogs() string {
	logs := strings.Join(r.logs, "\n") + "\n"
	r.logs = make([]string, 0)
	return logs
}

func (r *Runner) setupLogging(cmdStart chan bool) error {
	scan := func(f func() (io.ReadCloser, error)) error {
		reader, err := f()
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(reader)
		go func() {
			<-cmdStart // Block on command start
			for scanner.Scan() {
				line := scanner.Text()
				r.log(line)
			}
		}()
		return nil
	}
	if err := scan(r.cmd.StdoutPipe); err != nil {
		return err
	}
	if err := scan(r.cmd.StderrPipe); err != nil {
		return err
	}
	return nil
}
