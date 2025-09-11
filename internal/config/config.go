package config

import "time"

// Config holds all configuration for the cog runtime service
type Config struct {
	// Server configuration
	Host string
	Port int

	// Mode configuration
	UseProcedureMode      bool
	AwaitExplicitShutdown bool
	OneShot               bool

	// Directory configuration
	WorkingDirectory string
	UploadURL        string
	IPCUrl           string

	// Runner configuration
	MaxRunners                int
	PythonBinPath             string
	RunnerShutdownGracePeriod time.Duration

	// Cleanup configuration
	CleanupTimeout time.Duration

	// Environment configuration
	EnvSet   map[string]string
	EnvUnset []string

	// Force shutdown channel
	ForceShutdown chan<- struct{}
}
