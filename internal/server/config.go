package server

type Config struct {
	Host                  string `ff:"long: host, default: 0.0.0.0, usage: HTTP server host"`
	Port                  int    `ff:"long: port, default: 5000, usage: HTTP server port"`
	WorkingDir            string `ff:"long: working-dir, nodefault, usage: working directory"`
	AwaitExplicitShutdown bool   `ff:"long: await-explicit-shutdown, default: false, usage: await explicit shutdown"`
	UploadUrl             string `ff:"long: upload-url, nodefault, usage: output file upload URL"`
}
