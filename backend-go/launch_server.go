//go:build !desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"
)

func launchApplication() error {
	return runServer()
}

// runServer 保证监听失败、收到信号和正常退出都先回收资源，再由 main 决定退出码。
func runServer() (err error) {
	application, err := newApp()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, application.shutdown()) }()

	application.logStartupInfo()
	srv := application.newHTTPServer(fmt.Sprintf(":%d", application.envCfg.Port))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveUntilStopped(ctx, srv, 10*time.Second)
}
