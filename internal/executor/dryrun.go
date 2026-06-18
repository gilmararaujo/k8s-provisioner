package executor

import (
	"fmt"
	"strings"
)

// DryRunExecutor is a Null-Object CommandExecutor: it prints each command that
// would run and performs no host mutation. Read-style calls return empty output,
// so callers that branch on command output should short-circuit their wait loops
// when dry-run is active (see Provisioner.dryRun).
type DryRunExecutor struct{}

// Compile-time verification that DryRunExecutor implements CommandExecutor.
var _ CommandExecutor = DryRunExecutor{}

func (DryRunExecutor) Run(name string, args ...string) (string, error) {
	fmt.Printf("[dry-run] %s %s\n", name, strings.Join(args, " "))
	return "", nil
}

func (DryRunExecutor) RunWithOutput(name string, args ...string) error {
	fmt.Printf("[dry-run] %s %s\n", name, strings.Join(args, " "))
	return nil
}

func (DryRunExecutor) RunShell(command string) (string, error) {
	fmt.Printf("[dry-run] sh -c %s\n", command)
	return "", nil
}

func (DryRunExecutor) RunShellWithOutput(command string) error {
	fmt.Printf("[dry-run] sh -c %s\n", command)
	return nil
}

func (DryRunExecutor) RunShellWithStdin(command, stdin string) (string, error) {
	fmt.Printf("[dry-run] sh -c %s (with stdin)\n", command)
	return "", nil
}
