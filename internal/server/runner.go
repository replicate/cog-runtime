package server

type Runner interface {
	Cancel(string) error
	ExitCode() int
	Predict(*PredictionRequest) (chan *PredictionResponse, error)
	Schema() string
	SetupResult() SetupResult
	Shutdown() error
	Start() error
	Status() Status
}

func NewRunner(cfg *Config) (Runner, error) {
	if cfg.UseProcedureMode {
		return NewProcedureRunner(cfg)
	}

	return NewPredictionRunner(cfg)
}
