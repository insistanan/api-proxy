package intent

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// 只启动测试二进制里的无业务辅助进程，不启动向量服务。
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
	host.Stderr = os.Stderr
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
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	require.NoError(t, err)
	defer windows.CloseHandle(child)
	defer windows.TerminateProcess(child, 1)
	require.NoError(t, host.Process.Kill())
	state, err := windows.WaitForSingleObject(child, 5000)
	require.NoError(t, err)
	require.EqualValues(t, windows.WAIT_OBJECT_0, state, "宿主强退后向量子进程仍存活")
}
