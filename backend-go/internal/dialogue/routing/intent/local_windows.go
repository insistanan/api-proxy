package intent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func RunLocalProcessHelper(_ []string) (bool, int) { return false, 0 }

func startLocalProcess(executable string, args []string, dir string, diagnostic io.Writer) (_ *localProcess, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			err = errors.Join(err, windows.CloseHandle(job))
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	// Windows 10 起可在创建时加入 Job，消除 Start 与 Assign 之间的孤儿进程窗口。
	const procThreadAttributeJobList = 0x0002000d
	if err := attributes.Update(procThreadAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		if !owned {
			err = errors.Join(err, reader.Close())
		}
	}()
	defer writer.Close()
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	handles := []windows.Handle{windows.Handle(writer.Fd()), windows.Handle(null.Fd())}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, err
		}
	}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	command := []string{windows.EscapeArg(executable)}
	for _, arg := range args {
		command = append(command, windows.EscapeArg(arg))
	}
	commandLine, err := windows.UTF16PtrFromString(strings.Join(command, " "))
	if err != nil {
		return nil, err
	}
	directory, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[1], handles[1], handles[0]
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, commandLine, nil, nil, true, windows.CREATE_NO_WINDOW|windows.EXTENDED_STARTUPINFO_PRESENT, nil, directory, &startup.StartupInfo, &process); err != nil {
		return nil, err
	}
	owned = true
	threadErr := windows.CloseHandle(process.Thread)
	output := make(chan error, 1)
	go func() { _, err := io.Copy(diagnostic, reader); output <- errors.Join(err, reader.Close()) }()
	var mu sync.Mutex
	closed := false
	return &localProcess{
		Kill: func() error {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				return os.ErrProcessDone
			}
			return windows.TerminateJobObject(job, 1)
		},
		Wait: func() error {
			_, waitErr := windows.WaitForSingleObject(process.Process, windows.INFINITE)
			var code uint32
			codeErr := windows.GetExitCodeProcess(process.Process, &code)
			mu.Lock()
			closed = true
			closeErr := windows.CloseHandle(job) // 同时回收后代，确保诊断管道最终关闭。
			mu.Unlock()
			processErr := windows.CloseHandle(process.Process)
			if code != 0 {
				waitErr = errors.Join(waitErr, fmt.Errorf("退出码 %d", code))
			}
			return errors.Join(waitErr, codeErr, threadErr, closeErr, processErr, <-output)
		},
	}, nil
}
