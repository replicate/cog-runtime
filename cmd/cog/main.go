package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

type ServerCmd struct {
	Host                      string        `help:"Host address to bind the HTTP server to" default:"0.0.0.0" env:"COG_HOST"`
	Port                      int           `help:"Port number for the HTTP server" default:"5000" env:"COG_PORT"`
	UseProcedureMode          bool          `help:"Enable procedure mode for concurrent predictions" name:"use-procedure-mode" env:"COG_USE_PROCEDURE_MODE"`
	AwaitExplicitShutdown     bool          `help:"Wait for explicit shutdown signal instead of auto-shutdown" name:"await-explicit-shutdown" env:"COG_AWAIT_EXPLICIT_SHUTDOWN"`
	OneShot                   bool          `help:"Enable one-shot mode (single runner, wait for cleanup before ready)" name:"one-shot" env:"COG_ONE_SHOT"`
	UploadURL                 string        `help:"Base URL for uploading prediction output files" name:"upload-url" env:"COG_UPLOAD_URL"`
	WorkingDirectory          string        `help:"Override the working directory for predictions" name:"working-directory" env:"COG_WORKING_DIRECTORY"`
	RunnerShutdownGracePeriod time.Duration `help:"Grace period before force-killing prediction runners" name:"runner-shutdown-grace-period" default:"600s" env:"COG_RUNNER_SHUTDOWN_GRACE_PERIOD"`
	CleanupTimeout            time.Duration `help:"Maximum time to wait for process cleanup before hard exit" name:"cleanup-timeout" default:"10s" env:"COG_CLEANUP_TIMEOUT"`
}

type SchemaCmd struct{}

type TestCmd struct{}

type CLI struct {
	Server ServerCmd `cmd:"" help:"Start the Cog HTTP server for serving predictions"`
	Schema SchemaCmd `cmd:"" help:"Generate OpenAPI schema from model definition"`
	Test   TestCmd   `cmd:"" help:"Run model tests to verify functionality"`
}

var logger = util.CreateLogger("cog")

func (s *ServerCmd) Run() error {
	log := logger.Sugar()

	// One-shot mode requires procedure mode
	if s.OneShot && !s.UseProcedureMode {
		log.Errorw("one-shot mode requires procedure mode")
		return fmt.Errorf("one-shot mode requires procedure mode, use --use-procedure-mode")
	}

	// Procedure mode implies await explicit shutdown
	// i.e. Python process exit should not trigger shutdown
	if s.UseProcedureMode {
		s.AwaitExplicitShutdown = true
	}
	log.Infow("configuration",
		"use-procedure-mode", s.UseProcedureMode,
		"await-explicit-shutdown", s.AwaitExplicitShutdown,
		"one-shot", s.OneShot,
		"upload-url", s.UploadURL,
	)

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	log.Infow("starting Cog HTTP server", "addr", addr, "version", util.Version(), "pid", os.Getpid())

	var err error
	currentWorkingDirectory := s.WorkingDirectory
	if currentWorkingDirectory == "" {
		currentWorkingDirectory, err = os.Getwd()
		if err != nil {
			log.Errorw("failed to get current working directory", "error", err)
			return err
		}
	}

	forceShutdown := make(chan struct{}, 1)

	serverCfg := server.Config{
		UseProcedureMode:          s.UseProcedureMode,
		AwaitExplicitShutdown:     s.AwaitExplicitShutdown,
		OneShot:                   s.OneShot,
		IPCUrl:                    fmt.Sprintf("http://localhost:%d/_ipc", s.Port),
		UploadURL:                 s.UploadURL,
		WorkingDirectory:          currentWorkingDirectory,
		RunnerShutdownGracePeriod: s.RunnerShutdownGracePeriod,
		CleanupTimeout:            s.CleanupTimeout,
		ForceShutdown:             forceShutdown,
	}
	// FIXME: in non-procedure mode we do not support concurrency in a meaningful way, we
	// statically create the runner list sized at 1.
	if maxRunners, ok := os.LookupEnv("COG_MAX_RUNNERS"); ok && s.UseProcedureMode {
		if i, err := strconv.Atoi(maxRunners); err == nil {
			serverCfg.MaxRunners = i
		} else {
			log.Errorw("failed to parse COG_MAX_RUNNERS", "value", maxRunners)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h, err := server.NewHandler(serverCfg, cancel) //nolint:contextcheck // context passing not viable in current architecture
	if err != nil {
		log.Errorw("failed to create server handler", "error", err)
		return err
	}
	mux := server.NewServeMux(h, s.UseProcedureMode)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // TODO: is 5s too long? likely
	}
	go func() {
		<-ctx.Done()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Errorw("failed to shutdown server", "error", err)
			os.Exit(1)
		}
	}()
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		for {
			select {
			case sig := <-ch:
				if sig == syscall.SIGTERM && s.AwaitExplicitShutdown {
					log.Warnw("ignoring signal to stop", "signal", sig)
				} else {
					log.Infow("stopping Cog HTTP server", "signal", sig)
					if err := h.Stop(); err != nil {
						log.Errorw("failed to stop server handler", "error", err)
						os.Exit(1)
					}
				}
			case <-forceShutdown:
				log.Errorw("cleanup timeout reached, forcing ungraceful shutdown")
				os.Exit(1)
			}
		}
	}()
	if err := httpServer.ListenAndServe(); errors.Is(err, http.ErrServerClosed) {
		exitCode := h.ExitCode()
		if exitCode == 0 {
			log.Infow("shutdown completed normally")
		} else {
			log.Errorw("python runner exited with code", "code", exitCode)
		}
		return nil
	}
	return err
}

func (s *SchemaCmd) Run() error {
	log := logger.Sugar()

	wd, err := os.Getwd()
	if err != nil {
		log.Errorw("failed to get working directory", "error", err)
		return err
	}
	y, err := util.ReadCogYaml(wd)
	if err != nil {
		log.Errorw("failed to read cog.yaml", "error", err)
		return err
	}
	m, c, err := y.PredictModuleAndPredictor()
	if err != nil {
		log.Errorw("failed to parse predict", "error", err)
		return err
	}
	bin, err := exec.LookPath("python3")
	if err != nil {
		log.Errorw("failed to find python3", "error", err)
		return err
	}
	return syscall.Exec(bin, []string{bin, "-m", "coglet.schema", m, c}, os.Environ()) //nolint:gosec // expected subprocess launched with variable
}

func (t *TestCmd) Run() error {
	log := logger.Sugar()

	wd, err := os.Getwd()
	if err != nil {
		log.Errorw("failed to get working directory", "error", err)
		return err
	}
	y, err := util.ReadCogYaml(wd)
	if err != nil {
		log.Errorw("failed to read cog.yaml", "error", err)
		return err
	}
	m, c, err := y.PredictModuleAndPredictor()
	if err != nil {
		log.Errorw("failed to parse predict", "error", err)
		return err
	}
	bin, err := exec.LookPath("python3")
	if err != nil {
		log.Errorw("failed to find python3", "error", err)
		return err
	}
	return syscall.Exec(bin, []string{bin, "-m", "coglet.test", m, c}, os.Environ()) //nolint:gosec // expected subprocess launched with variable
}

func main() {
	log := logger.Sugar()

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("cog"),
		kong.Description("Cog runtime for serving machine learning models via HTTP API"),
		kong.UsageOnError(),
	)

	err := ctx.Run()
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
