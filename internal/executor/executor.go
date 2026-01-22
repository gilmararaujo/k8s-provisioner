package executor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Executor struct {
	Verbose bool
}

func New(verbose bool) *Executor {
	return &Executor{Verbose: verbose}
}

// Run executes a command and returns the output
func (e *Executor) Run(name string, args ...string) (string, error) {
	if e.Verbose {
		fmt.Printf(">>> %s %s\n", name, strings.Join(args, " "))
	}

	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// RunWithOutput executes a command and streams output to stdout
func (e *Executor) RunWithOutput(name string, args ...string) error {
	if e.Verbose {
		fmt.Printf(">>> %s %s\n", name, strings.Join(args, " "))
	}

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// RunShell executes a shell command
func (e *Executor) RunShell(command string) (string, error) {
	if e.Verbose {
		fmt.Printf(">>> sh -c %s\n", command)
	}

	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// RunShellWithOutput executes a shell command and streams output
func (e *Executor) RunShellWithOutput(command string) error {
	if e.Verbose {
		fmt.Printf(">>> sh -c %s\n", command)
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

// AppendToFile appends content to a file
func AppendToFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}