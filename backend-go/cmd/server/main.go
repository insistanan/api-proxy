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
	"insistanan/api-proxy/internal/dialogue/routing/intent"
	"insistanan/api-proxy/internal/infra/cli"
	"insistanan/api-proxy/internal/infra/config"
)

func main() {
	if handled, code := intent.RunLocalProcessHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
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

// runServer 给完整停机设置期限。到期由 main 以失败状态退出进程，
// 不在请求或生产者仍运行时抢先关闭它们依赖的数据库。
func runServer() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- runApplication(ctx) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
	}
	// 恢复信号默认行为，第二次 Ctrl+C 可以立即终止卡住的 I/O。
	stop()
	return awaitShutdown(result, 30*time.Second)
}

func awaitShutdown(result <-chan error, budget time.Duration) error {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		return fmt.Errorf("完整停机超过 %s，仍有请求或资源未退出，强制结束进程；未落盘数据可能丢失", budget)
	}
}

func runApplication(ctx context.Context) (err error) {
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
	return serveUntilStopped(ctx, srv, 10*time.Second)
}
