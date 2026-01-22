package version

import (
	"fmt"
	"runtime"
)

// Variáveis injetadas em tempo de build via -ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Info retorna informações completas da versão
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get retorna as informações de versão
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String retorna a versão formatada
func (i Info) String() string {
	return fmt.Sprintf(`k8s-provisioner %s
  Git Commit: %s
  Build Date: %s
  Go Version: %s
  Platform:   %s`,
		i.Version,
		i.GitCommit,
		i.BuildDate,
		i.GoVersion,
		i.Platform,
	)
}

// Short retorna apenas a versão
func (i Info) Short() string {
	return i.Version
}