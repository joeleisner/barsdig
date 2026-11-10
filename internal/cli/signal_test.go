//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Ctrl+C must stop index and export (only serve traps it for a graceful shutdown).
func TestInterruptStopsExport(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "barsdig")
	if out, err := exec.Command("go", "build", "-o", bin, "barsdig/cmd/barsdig").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := romfs(t, nil)
	fifo := filepath.Join(t.TempDir(), "picks")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	cmd := exec.Command(bin, "export", root, "--out", t.TempDir(), "--picks", fifo, "--cache-dir", "")
	if err := cmd.Start(); err != nil { // blocks opening the fifo: stands in for a long-running export
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("SIGINT did not stop 'barsdig export'")
	}
}
