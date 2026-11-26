//go:build !windows

package intent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

const localWorkerFlag = "--internal-embedding-worker"

// Unix 通过专用监督进程持有子进程。宿主正常或异常退出都会关闭管道，
// 监督进程收到 EOF 后回收自己启动的运行时，不按进程名寻找或杀死其他服务。
func startLocalProcess(executable string, args []string, dir string, diagnostic io.Writer) (*localProcess, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self, append([]string{localWorkerFlag, executable}, args...)...)
	cmd.Dir, cmd.Stderr = dir, diagnostic
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, pipe.Close())
	}
	var once sync.Once
	var closeErr error
	closePipe := func() error { once.Do(func() { closeErr = pipe.Close() }); return closeErr }
	return &localProcess{Wait: func() error { return errors.Join(cmd.Wait(), closePipe()) }, Kill: closePipe}, nil
}

// RunLocalProcessHelper 仅由进程入口在加载配置和启动服务之前调用。
func RunLocalProcessHelper(args []string) (bool, int) {
	if len(args) == 0 || args[0] != localWorkerFlag {
		return false, 0
	}
	if len(args) < 2 {
		return true, 2
	}
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	var mu sync.Mutex
	stopped := false
	go func() {
		_, readErr := io.Copy(io.Discard, os.Stdin)
		mu.Lock()
		defer mu.Unlock()
		stopped = true
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "读取宿主生命周期管道失败:", readErr)
		}
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				fmt.Fprintln(os.Stderr, "回收本地助手失败:", err)
			}
		}
	}()
	mu.Lock()
	if stopped {
		mu.Unlock()
		return true, 1
	}
	err := cmd.Start()
	mu.Unlock()
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "本地助手退出:", err)
		return true, 1
	}
	return true, 0
}
