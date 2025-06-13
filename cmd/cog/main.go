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
	"github.com/replicate/go/logging"
	"github.com/replicate/go/must"

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

var logger = logging.New("cog")

func schemaCommand() *ff.Command {
	log := logger.Sugar()

	flags := ff.NewFlagSet("schema")

	return &ff.Command{
		Name:  "schema",
		Usage: "schema [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			y, err := util.ReadCogYaml(must.Get(os.Getwd()))
			if err != nil {
				log.Errorw("failed to read cog.yaml", "err", err)
				return err
			}
			m, c, err := y.PredictModuleAndPredictor()
			if err != nil {
				log.Errorw("failed to parse predict", "err", err)
				return err
			}
			bin := must.Get(exec.LookPath("python3"))
			return syscall.Exec(bin, []string{bin, "-m", "coglet.schema", m, c}, os.Environ())
		},
	}
}

func serverCommand() *ff.Command {
	log := logger.Sugar()

	var cfg ServerConfig
	flags := ff.NewFlagSet("server")
	must.Do(flags.AddStruct(&cfg))

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

			ctx, cancel := context.WithCancel(ctx)
			go func() {
				ch := make(chan os.Signal, 1)
				signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
				s := <-ch
				log.Infow("stopping Cog HTTP server", "signal", s)
				cancel()
			}()

			addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
			log.Infow("starting Cog HTTP server", "addr", addr, "version", util.Version())
			serverCfg := server.Config{
				UseProcedureMode:      cfg.UseProcedureMode,
				AwaitExplicitShutdown: cfg.AwaitExplicitShutdown,
				UploadUrl:             cfg.UploadUrl,
			}
			h := server.NewHandler(serverCfg)
			s := server.NewServer(addr, h, cfg.UseProcedureMode)
			go func() {
				<-ctx.Done()
				must.Do(h.Stop())
				must.Do(s.Shutdown(ctx))
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
	}
}

func testCommand() *ff.Command {
	log := logger.Sugar()

	flags := ff.NewFlagSet("test")

	return &ff.Command{
		Name:  "test",
		Usage: "test [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			y, err := util.ReadCogYaml(must.Get(os.Getwd()))
			if err != nil {
				log.Errorw("failed to read cog.yaml", "err", err)
				return err
			}
			m, c, err := y.PredictModuleAndPredictor()
			if err != nil {
				log.Errorw("failed to parse predict", "err", err)
				return err
			}
			bin := must.Get(exec.LookPath("python3"))
			return syscall.Exec(bin, []string{bin, "-m", "coglet.test", m, c}, os.Environ())
		},
	}
}

func main() {
	log := logger.Sugar()
	flags := ff.NewFlagSet("cog")
	cmd := &ff.Command{
		Name:  "cog",
		Usage: "cog <COMMAND> [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			return ff.ErrHelp
		},
		Subcommands: []*ff.Command{
			schemaCommand(),
			serverCommand(),
			testCommand(),
		},
	}
	err := cmd.ParseAndRun(context.Background(), os.Args[1:])
	switch {
	case errors.Is(err, ff.ErrHelp):
		must.Get(fmt.Fprintln(os.Stderr, ffhelp.Command(cmd)))
		os.Exit(1)
	case err != nil:
		log.Error(err)
		os.Exit(1)
	}
}
