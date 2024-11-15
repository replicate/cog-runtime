package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"github.com/replicate/go/logging"
	"github.com/replicate/go/must"
	"github.com/replicate/go/version"
	_ "go.uber.org/automaxprocs"

	"github.com/replicate/coggo/internal/server"
	"github.com/replicate/coggo/internal/util"
)

var logger = logging.New("cog-http-server")

type Config struct {
	Host                  string `ff:"long: host, default: 0.0.0.0, usage: HTTP server host"`
	Port                  int    `ff:"long: port, default: 5000, usage: HTTP server port"`
	WorkingDir            string `ff:"long: working-dir, nodefault, usage: working directory"`
	ModuleName            string `ff:"long: module-name, nodefault, usage: Python module name"`
	ClassName             string `ff:"long: class-name, nodefault, usage: Python class name"`
	AwaitExplicitShutdown bool   `ff:"long: await-explicit-shutdown, default: false, usage: await explicit shutdown"`
}

func (c *Config) Validate() error {
	if (c.ModuleName == "") != (c.ClassName == "") {
		return fmt.Errorf("--module-name and --class-name must be specified together")
	}
	return nil
}

func main() {
	log := logger.Sugar()

	var cfg Config
	flags := ff.NewFlagSet("cog-http-server")
	must.Do(flags.AddStruct(&cfg))

	cmd := &ff.Command{
		Name:  "cog-http-server",
		Usage: "cog-http-server [FLAGS]",
		Flags: flags,
		Exec: func(ctx context.Context, args []string) error {
			if err := cfg.Validate(); err != nil {
				return err
			}

			var moduleName, className string
			if cfg.ModuleName != "" && cfg.ClassName != "" {
				moduleName, className = cfg.ModuleName, cfg.ClassName
			} else {
				m, c, err := util.PredictFromCogYaml()
				if err != nil {
					return err
				}
				moduleName, className = m, c
			}
			workingDir := cfg.WorkingDir
			if workingDir == "" {
				workingDir = must.Get(os.MkdirTemp("", "cog-http-server-"))
			}
			log.Infow("configuration", "working-dir", workingDir, "module-name", moduleName, "class-name", className)

			addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
			log.Infow("starting HTTP server", "addr", addr)
			r := server.NewRunner(workingDir, moduleName, className, cfg.AwaitExplicitShutdown)
			must.Do(r.Start())
			s := server.NewServer(addr, r)
			go func() {
				<-ctx.Done()
				must.Do(r.Shutdown())
				must.Do(s.Shutdown(ctx))
			}()
			if err := s.ListenAndServe(); errors.Is(err, http.ErrServerClosed) {
				if r.ExitCode() == 0 {
					return nil
				} else {
					return fmt.Errorf("python runner exited with code %d", r.ExitCode())
				}
			} else {
				return err
			}
		},
	}

	err := cmd.Parse(os.Args[1:])
	switch {
	case errors.Is(err, ff.ErrHelp):
		must.Get(fmt.Fprintln(os.Stderr, ffhelp.Command(cmd)))
		os.Exit(1)
	case err != nil:
		log.Error(err)
		must.Get(fmt.Fprintln(os.Stderr, ffhelp.Command(cmd)))
		os.Exit(1)
	}

	log.Infow("starting Cog HTTP server", "version", version.Version())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		s := <-ch
		log.Infow("stopping Cog HTTP server", "signal", s)
		cancel()
	}()
	if err := cmd.Run(ctx); err != nil {
		log.Error(err)
	} else {
		log.Info("shutdown completed normally")
	}
}
