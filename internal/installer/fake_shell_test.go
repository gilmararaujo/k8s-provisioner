package installer

import "strings"

// fakeShell implements executor.ShellExecutor for unit tests. outputs maps a
// command substring to the stdout to return; errs maps a substring to the error
// to return. calls records every command in order so sequencing can be asserted.
type fakeShell struct {
	calls   []string
	outputs map[string]string
	errs    map[string]error
}

func (f *fakeShell) RunShell(cmd string) (string, error) {
	f.calls = append(f.calls, cmd)
	for sub, out := range f.outputs {
		if strings.Contains(cmd, sub) {
			return out, f.errs[sub]
		}
	}
	return "", nil
}

func (f *fakeShell) RunShellWithOutput(cmd string) error {
	f.calls = append(f.calls, cmd)
	return nil
}

func (f *fakeShell) RunShellWithStdin(cmd, _ string) (string, error) {
	f.calls = append(f.calls, cmd)
	return "", nil
}
