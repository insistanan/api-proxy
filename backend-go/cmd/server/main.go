package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"insistanan/api-proxy/internal/api/system"
	"insistanan/api-proxy/internal/infra/cli"
	"insistanan/api-proxy/internal/infra/config"
)

func main() {
	loadedEnvFiles, err := config.LoadDotEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(loadedEnvFiles) == 0 {
		log.Println("没有找到 .env 文件，使用环境变量或默认值")
	} else {
		log.Printf("[Env-Load] 已加载: %s", strings.Join(loadedEnvFiles, ", "))
	}

	if len(os.Args) > 1 && os.Args[1] == "logs" {
		if err := cli.RunLogs(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	// 设置系统 API 的版本信息。
	system.SetVersionInfo(Version, BuildTime, GitCommit)

	if err := runServer(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// runServer 保证监听失败、收到信号和正常退出都先回收资源，再由 main 决定退出码。
func runServer() (err error) {
	application, err := newApp()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, application.shutdown()) }()

	addr := fmt.Sprintf(":%d", application.envCfg.Port)
	application.logStartupInfo()
	srv := &http.Server{
		Addr:              addr,
		Handler:           application.engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveUntilStopped(ctx, srv, 10*time.Second)
}
