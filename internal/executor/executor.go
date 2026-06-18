package executor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	// sshpassRe matches `-p '<password>'` as used in sshpass invocations.
	sshpassRe = regexp.MustCompile(`-p '[^']*'`)
	// vaultTokenRe matches a Vault token passed as an HTTP header (stopping at the
	// next quote or whitespace so surrounding shell quoting is preserved).
	vaultTokenRe = regexp.MustCompile(`(?i)(X-Vault-Token:\s*)[^\s'"]+`)
)

// scrub redacts credential material (sshpass passwords, Vault tokens) so it does
// not leak into error messages or verbose command echoes. Applied to every
// command string we print and to stderr we surface in errors.
func scrub(s string) string {
	s = sshpassRe.ReplaceAllString(s, "-p '***'")
	s = vaultTokenRe.ReplaceAllString(s, "${1}***")
	return s
}

// ShellExecutor defines shell-command execution. It is the narrow surface the
// installers depend on (they never use the argv-form Run/RunWithOutput), per ISP.
type ShellExecutor interface {
	// RunShell runs `sh -c command` and returns stdout. Implementations that do
	// not execute (e.g. dry-run) return "" and callers must not depend on output.
	RunShell(command string) (string, error)
	RunShellWithOutput(command string) error
	RunShellWithStdin(command string, stdin string) (string, error)
}

// CommandExecutor adds the argv-form helpers used by host-level provisioning
// (modprobe, systemctl, …) on top of ShellExecutor.
type CommandExecutor interface {
	ShellExecutor
	Run(name string, args ...string) (string, error)
	RunWithOutput(name string, args ...string) error
}

// Executor implements CommandExecutor
type Executor struct {
	Verbose bool
}

// Compile-time verification that Executor implements CommandExecutor
var _ CommandExecutor = (*Executor)(nil)

func New(verbose bool) *Executor {
	return &Executor{Verbose: verbose}
}

// Run executes a command and returns the output
func (e *Executor) Run(name string, args ...string) (string, error) {
	if e.Verbose {
		fmt.Printf(">>> %s\n", scrub(name+" "+strings.Join(args, " ")))
	}

	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, scrub(stderr.String()))
	}

	return stdout.String(), nil
}

// RunWithOutput executes a command and streams output to stdout
func (e *Executor) RunWithOutput(name string, args ...string) error {
	if e.Verbose {
		fmt.Printf(">>> %s\n", scrub(name+" "+strings.Join(args, " ")))
	}

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// RunShell executes a shell command
func (e *Executor) RunShell(command string) (string, error) {
	if e.Verbose {
		fmt.Printf(">>> sh -c %s\n", scrub(command))
	}

	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, scrub(stderr.String()))
	}

	return stdout.String(), nil
}

// RunShellWithOutput executes a shell command and streams output
func (e *Executor) RunShellWithOutput(command string) error {
	if e.Verbose {
		fmt.Printf(">>> sh -c %s\n", scrub(command))
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// FileExists checks if a file exists
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteFile writes content to a file
func WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// ReadFileContents reads a file and returns its content as string
func ReadFileContents(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RunShellWithStdin executes a shell command with stdin input
func (e *Executor) RunShellWithStdin(command string, stdin string) (string, error) {
	if e.Verbose {
		fmt.Printf(">>> sh -c %s (with stdin)\n", scrub(command))
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, scrub(stderr.String()))
	}

	return stdout.String(), nil
}

// AppendToFile appends content to a file
func AppendToFile(path, content string) (err error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	if _, err := f.WriteString(content); err != nil {
		return err
	}
	return nil
}
