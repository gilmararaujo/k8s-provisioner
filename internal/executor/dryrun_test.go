package executor

import "testing"

func TestDryRunExecutor_NoMutationEmptyOutput(t *testing.T) {
	var e CommandExecutor = DryRunExecutor{}

	if out, err := e.RunShell("rm -rf /tmp/whatever"); out != "" || err != nil {
		t.Fatalf("RunShell: want \"\",nil got %q,%v", out, err)
	}
	if out, err := e.Run("systemctl", "start", "crio"); out != "" || err != nil {
		t.Fatalf("Run: want \"\",nil got %q,%v", out, err)
	}
	if err := e.RunShellWithOutput("kubeadm init"); err != nil {
		t.Fatalf("RunShellWithOutput: want nil got %v", err)
	}
	if out, err := e.RunShellWithStdin("kubectl apply -f -", "manifest"); out != "" || err != nil {
		t.Fatalf("RunShellWithStdin: want \"\",nil got %q,%v", out, err)
	}
}
