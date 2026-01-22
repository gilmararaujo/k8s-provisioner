package version

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	info := Get()

	assert.NotEmpty(t, info.Version, "Version should not be empty")
	assert.NotEmpty(t, info.GitCommit, "GitCommit should not be empty")
	assert.NotEmpty(t, info.BuildDate, "BuildDate should not be empty")
	assert.Equal(t, runtime.Version(), info.GoVersion, "GoVersion should match runtime.Version()")
	assert.NotEmpty(t, info.Platform, "Platform should not be empty")
	assert.Contains(t, info.Platform, "/", "Platform should contain OS/Arch separator")
}

func TestString(t *testing.T) {
	info := Get()
	str := info.String()

	assert.Contains(t, str, "k8s-provisioner", "String should contain project name")
	assert.Contains(t, str, info.Version, "String should contain version")
	assert.Contains(t, str, "Git Commit:", "String should contain Git Commit label")
	assert.Contains(t, str, info.GitCommit, "String should contain git commit value")
	assert.Contains(t, str, "Build Date:", "String should contain Build Date label")
	assert.Contains(t, str, info.BuildDate, "String should contain build date value")
	assert.Contains(t, str, "Go Version:", "String should contain Go Version label")
	assert.Contains(t, str, info.GoVersion, "String should contain go version value")
	assert.Contains(t, str, "Platform:", "String should contain Platform label")
	assert.Contains(t, str, info.Platform, "String should contain platform value")
}

func TestShort(t *testing.T) {
	info := Get()
	short := info.Short()

	assert.Equal(t, info.Version, short, "Short should return only version")
	assert.False(t, strings.Contains(short, "\n"), "Short should not contain newlines")
}

func TestInfoFields(t *testing.T) {
	// Test with custom values
	originalVersion := Version
	originalCommit := GitCommit
	originalDate := BuildDate

	defer func() {
		Version = originalVersion
		GitCommit = originalCommit
		BuildDate = originalDate
	}()

	Version = "v1.0.0"
	GitCommit = "abc123"
	BuildDate = "2024-01-01"

	info := Get()

	assert.Equal(t, "v1.0.0", info.Version)
	assert.Equal(t, "abc123", info.GitCommit)
	assert.Equal(t, "2024-01-01", info.BuildDate)
}
