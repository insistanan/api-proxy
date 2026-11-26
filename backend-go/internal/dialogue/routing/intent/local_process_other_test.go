//go:build !windows

package intent

import (
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, code := RunLocalProcessHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestOwnedRuntimeHelper(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-3] != "--owned-test" {
		return
	}
	mode, path := args[len(args)-2], args[len(args)-1]
	if mode == "child" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
		return
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	process, err := startLocalProcess(self, []string{"-test.run=^TestOwnedRuntimeHelper$", "--", "--owned-test", "child", path}, os.TempDir(), io.Discard)
	if err != nil {
		os.Exit(4)
	}
	if err := process.Wait(); err != nil {
		os.Exit(5)
	}
}

func TestOwnedRuntime_HostTerminationReapsChild(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	path := t.TempDir() + "/child.pid"
	host := exec.Command(self, "-test.run=^TestOwnedRuntimeHelper$", "--", "--owned-test", "host", path)
	require.NoError(t, host.Start())
	t.Cleanup(func() { _ = host.Process.Kill(); _ = host.Wait() })
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(string(data))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.NoError(t, host.Process.Kill())
	require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }, 5*time.Second, 10*time.Millisecond)
}
