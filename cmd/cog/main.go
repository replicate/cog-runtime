package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	_ "go.uber.org/automaxprocs"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

type ServerConfig struct {
	Host                  string `ff:"long: host, default: 0.0.0.0, usage: HTTP server host"`
	Port                  int    `ff:"long: port, default: 5000, usage: HTTP server port"`
	UseProcedureMode      bool   `ff:"long: use-procedure-mode, default: false, usage: use-procedure mode"`
	AwaitExplicitShutdown bool   `ff:"long: await-explicit-shutdown, default: false, usage: await explicit shutdown"`
	UploadUrl             string `ff:"long: upload-url, nodefault, usage: output file upload URL"`
}

var logger = util.CreateLogger("cog")

func schemaCommand() *ff.Command {
	log := logger.Sugar()

	flags := ff.NewFlagSet("schema")

	return &ff.Command{
		Name:  "schema",
		Usage: "schema [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
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
			return syscall.Exec(bin, []string{bin, "-m", "coglet.schema", m, c}, os.Environ())
		},
	}
}

func serverCommand() (*ff.Command, error) {
	log := logger.Sugar()

	var cfg ServerConfig
	flags := ff.NewFlagSet("server")

	if err := flags.AddStruct(&cfg); err != nil {
		return nil, err
	}

	return &ff.Command{
		Name:  "server",
		Usage: "server [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			// Procedure mode implies await explicit shutdown
			// i.e. Python process exit should not trigger shutdown
			if cfg.UseProcedureMode {
				cfg.AwaitExplicitShutdown = true
			}
			log.Infow("configuration",
				"use-procedure-mode", cfg.UseProcedureMode,
				"await-explicit-shutdown", cfg.AwaitExplicitShutdown,
				"upload-url", cfg.UploadUrl,
			)

			addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
			log.Infow("starting Cog HTTP server", "addr", addr, "version", util.Version(), "pid", os.Getpid())
			serverCfg := server.Config{
				UseProcedureMode:      cfg.UseProcedureMode,
				AwaitExplicitShutdown: cfg.AwaitExplicitShutdown,
				IPCUrl:                fmt.Sprintf("http://localhost:%d/_ipc", cfg.Port),
				UploadUrl:             cfg.UploadUrl,
			}
			ctx, cancel := context.WithCancel(ctx)
			h, err := server.NewHandler(serverCfg, cancel)
			if err != nil {
				log.Errorw("failed to create server handler", "error", err)
				return err
			}
			s := server.NewServer(addr, h, cfg.UseProcedureMode)
			go func() {
				<-ctx.Done()
				if err := s.Shutdown(ctx); err != nil {
					log.Errorw("failed to shutdown server", "error", err)
					os.Exit(1)
				}
			}()
			go func() {
				ch := make(chan os.Signal, 1)
				signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
				for {
					sig := <-ch
					if sig == syscall.SIGTERM && cfg.AwaitExplicitShutdown {
						log.Warnw("ignoring signal to stop", "signal", sig)
					} else {
						log.Infow("stopping Cog HTTP server", "signal", sig)
						if err := h.Stop(); err != nil {
							log.Errorw("failed to stop server handler", "error", err)
							os.Exit(1)
						}
					}
				}
			}()
			if err := s.ListenAndServe(); errors.Is(err, http.ErrServerClosed) {
				exitCode := h.ExitCode()
				if exitCode == 0 {
					log.Infow("shutdown completed normally")
				} else {
					log.Errorw("python runner exited with code", "code", exitCode)
				}
				return nil
			} else {
				return err
			}
		},
	}, nil
}

func testCommand() *ff.Command {
	log := logger.Sugar()

	flags := ff.NewFlagSet("test")

	return &ff.Command{
		Name:  "test",
		Usage: "test [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
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
			return syscall.Exec(bin, []string{bin, "-m", "coglet.test", m, c}, os.Environ())
		},
	}
}

func main() {
	log := logger.Sugar()
	flags := ff.NewFlagSet("cog")
	serverCommand, err := serverCommand()
	if err != nil {
		log.Errorw("failed to create server command", "error", err)
		os.Exit(1)
	}
	cmd := &ff.Command{
		Name:  "cog",
		Usage: "cog <COMMAND> [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			return ff.ErrHelp
		},
		Subcommands: []*ff.Command{
			schemaCommand(),
			serverCommand,
			testCommand(),
		},
	}
	err = cmd.ParseAndRun(context.Background(), os.Args[1:])
	switch {
	case errors.Is(err, ff.ErrHelp):
		_, err := fmt.Fprintln(os.Stderr, ffhelp.Command(cmd))
		if err != nil {
			log.Errorw("failed to print help", "error", err)
		}
		os.Exit(1)
	case err != nil:
		log.Error(err)
		os.Exit(1)
	}
}
