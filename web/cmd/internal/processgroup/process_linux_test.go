//go:build linux

package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupHelper(t *testing.T) {
	if os.Getenv("PIXIE_PROCESS_GROUP_HELPER") != "1" {
		return
	}
	pidPath := os.Getenv("PIXIE_PROCESS_GROUP_CHILD_PID")
	child := exec.Command(os.Args[0], "-test.run=TestProcessGroupHelper", "--")
	child.Env = append(os.Environ(), "PIXIE_PROCESS_GROUP_HELPER=child")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(3)
	}
	select {}
}

func TestRunDrainsDescendantProcessGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	signals := make(chan os.Signal, 1)
	result := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := Run(Invocation{
			Path: os.Args[0],
			Args: []string{"-test.run=TestProcessGroupHelper", "--"},
			Environment: append(
				os.Environ(),
				"PIXIE_PROCESS_GROUP_HELPER=1",
				"PIXIE_PROCESS_GROUP_CHILD_PID="+pidPath,
			),
		}, signals)
		result <- struct {
			code int
			err  error
		}{code, err}
	}()
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidPath)
		if err == nil {
			pid, err = strconv.Atoi(string(raw))
			if err == nil && pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("helper never started its descendant")
	}
	signals <- syscall.SIGTERM
	select {
	case outcome := <-result:
		if outcome.err == nil {
			t.Fatal("terminated child was reported as a clean command result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process group was not reaped after SIGTERM")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d remained after group drain", pid)
}
