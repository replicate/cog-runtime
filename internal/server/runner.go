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
	"time"

	"github.com/replicate/go/must"

	"github.com/replicate/cog-runtime/internal/util"
)

var LOG_REGEX = regexp.MustCompile(`^\[pid=(?P<pid>[^\\]+)] (?P<msg>.*)$`)
var RESPONSE_REGEX = regexp.MustCompile(`^response-(?P<pid>\S+)-(?P<epoch>\d+).json$`)

type PendingPrediction struct {
	request     PredictionRequest
	response    PredictionResponse
	lastUpdated time.Time
	inputPaths  []string
	mu          sync.Mutex
	c           chan PredictionResponse
}

func (pr *PendingPrediction) appendLogLine(line string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.response.Logs += fmt.Sprintln(line)
}

func (pr *PendingPrediction) sendWebhook() {
	pr.mu.Lock()
	if pr.request.Webhook == "" {
		pr.mu.Unlock()
		return
	}
	if pr.response.Status == PredictionProcessing {
		if time.Since(pr.lastUpdated) < 500*time.Millisecond {
			pr.mu.Unlock()
			return
		}
		pr.lastUpdated = time.Now()
	}
	pr.mu.Unlock()

	log := logger.Sugar()
	log.Infow("sending webhook", "url", pr.request.Webhook, "response", pr.response)
	body := bytes.NewBuffer(must.Get(json.Marshal(pr.response)))
	req := must.Get(http.NewRequest("POST", pr.request.Webhook, body))
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Errorw("failed to send webhook", "error", err)
	} else if resp.StatusCode != 200 {
		body := string(must.Get(io.ReadAll(resp.Body)))
		log.Errorw("failed to send webhook", "code", resp.StatusCode, "body", body)
	}
}

func (pr *PendingPrediction) sendResponse() {
	if pr.c == nil {
		return
	}
	pr.c <- pr.response
}

type Runner struct {
	workingDir            string
	cmd                   exec.Cmd
	status                Status
	schema                string
	setupResult           SetupResult
	logs                  []string
	pending               map[string]*PendingPrediction
	awaitExplicitShutdown bool
	shutdownRequested     bool
	mu                    sync.Mutex
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
		pending:               make(map[string]*PendingPrediction),
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
	r.setupResult = SetupResult{
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
	r.mu.Lock()
	r.shutdownRequested = true
	r.mu.Unlock()
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
	if req.CreatedAt == "" {
		req.CreatedAt = util.NowIso()
	}
	r.mu.Lock()
	if _, ok := r.pending[req.Id]; ok {
		r.mu.Unlock()
		log.Errorw("prediction rejected: prediction ID exists", "id", req.Id)
		return nil, fmt.Errorf("prediction ID exists")
	}
	r.mu.Unlock()

	log.Infow("received prediction request", "id", req.Id)

	inputPaths := make([]string, 0)
	input, err := handlePath(req.Input, &inputPaths, base64ToInput)
	if err != nil {
		return nil, err
	}
	req.Input = input

	reqPath := path.Join(r.workingDir, fmt.Sprintf("request-%s.json", req.Id))
	must.Do(os.WriteFile(reqPath, must.Get(json.Marshal(req)), 0644))
	resp := PredictionResponse{
		Input:     req.Input,
		Id:        req.Id,
		CreatedAt: req.CreatedAt,
	}
	pr := PendingPrediction{
		request:    req,
		response:   resp,
		inputPaths: inputPaths,
	}
	if req.Webhook == "" {
		pr.c = make(chan PredictionResponse, 1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[req.Id] = &pr
	return pr.c, nil
}

////////////////////
// Background tasks

func (r *Runner) wait() {
	log := logger.Sugar()
	err := r.cmd.Wait()
	if err != nil {
		runnerLogs := r.rotateLogs()
		log.Errorw("python runner excited with error", "pid", r.cmd.Process.Pid, "error", err, "logs", runnerLogs)
		for _, pr := range r.pending {
			pr.mu.Lock()
			now := util.NowIso()
			if pr.response.StartedAt == "" {
				pr.response.StartedAt = now
			}
			pr.response.CompletedAt = now
			pr.response.Error = runnerLogs
			pr.response.Status = PredictionFailed
			pr.mu.Unlock()

			pr.sendWebhook()
			pr.sendResponse()
		}
		r.mu.Lock()
		if r.status == StatusStarting {
			r.status = StatusSetupFailed
			r.setupResult.CompletedAt = util.NowIso()
			r.setupResult.Status = SetupFailed
			r.setupResult.Logs = runnerLogs
		} else {
			r.status = StatusDefunct
		}
		r.mu.Unlock()
	} else {
		log.Infow("python runner exited successfully", "pid", r.cmd.Process.Pid)
		r.mu.Lock()
		r.status = StatusDefunct
		r.mu.Unlock()
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
			r.mu.Lock()
			r.status = StatusReady
			r.mu.Unlock()
		} else if s == SigBusy {
			log.Info("runner is busy")
			r.mu.Lock()
			r.status = StatusBusy
			r.mu.Unlock()
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
	logs := r.rotateLogs()
	r.mu.Lock()
	defer r.mu.Unlock()
	must.Do(r.readJson("setup_result.json", &r.setupResult))
	r.setupResult.Logs = logs
	if r.setupResult.Status == SetupSucceeded {
		log.Infow("setup succeeded")
		r.status = StatusReady
	} else if r.setupResult.Status == SetupFailed {
		log.Errorw("setup failed")
		r.status = StatusSetupFailed
	} else {
		panic(fmt.Sprintf("invalid setup status: %s", r.setupResult.Status))
	}
}

func (r *Runner) handleResponses() {
	log := logger.Sugar()
	for _, entry := range must.Get(os.ReadDir(r.workingDir)) {
		m := RESPONSE_REGEX.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		pid := m[1]
		r.mu.Lock()
		pr, ok := r.pending[pid]
		if !ok {
			r.mu.Unlock()
			continue
		}
		r.mu.Unlock()

		pr.mu.Lock()
		log.Infow("received prediction response", "id", pid)
		must.Do(r.readJson(entry.Name(), &pr.response))
		// Delete response immediately to avoid duplicates
		must.Do(os.Remove(path.Join(r.workingDir, entry.Name())))

		paths := make([]string, 0)
		if output, err := handlePath(pr.response.Output, &paths, outputToBase64); err != nil {
			log.Errorw("failed to handle output", "id", pid, "error", err)
			pr.response.Error = err.Error()
		} else {
			pr.response.Output = output
		}
		for _, p := range paths {
			must.Do(os.Remove(p))
		}
		pr.mu.Unlock()

		pr.sendWebhook()
		completed := false
		if pr.response.Status == PredictionStarting {
			log.Infow("prediction started", "id", pr.request.Id, "status", pr.response.Status)
			// Only async and iterator predict writes new response per output item with status = "processing"
			// For blocking or non-iterator cases, set it here immediately after sending "starting" webhook
			pr.mu.Lock()
			pr.response.Status = PredictionProcessing
			pr.mu.Unlock()
		} else if _, ok := PredictionCompletedStatuses[pr.response.Status]; ok {
			log.Infow("prediction completed", "id", pr.request.Id, "status", pr.response.Status)
			pr.sendResponse()
			completed = true
		}
		if completed {
			for _, p := range pr.inputPaths {
				must.Do(os.Remove(p))
			}
			r.mu.Lock()
			delete(r.pending, pid)
			r.mu.Unlock()
		}
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

////////////////////
// Log handling

func (r *Runner) log(line string) {
	log := logger.Sugar()
	if m := LOG_REGEX.FindStringSubmatch(line); m != nil {
		pid := m[1]
		msg := m[2]
		r.mu.Lock()
		defer r.mu.Unlock()
		if pr, ok := r.pending[pid]; ok {
			pr.appendLogLine(msg)
			// In case log is received before "starting" response
			if pr.response.Status != "" {
				pr.sendWebhook()
			}
		} else {
			log.Errorw("received log for non-existent prediction", "id", pid, "message", msg)
		}
	} else if !strings.Contains(line, "[coglet]") {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, line)
	}
	fmt.Println(line)
}

func (r *Runner) rotateLogs() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs := util.JoinLogs(r.logs)
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
