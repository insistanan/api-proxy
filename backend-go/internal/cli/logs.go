package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/logger"
)

const logsUsage = `用法:
  api-proxy logs query --from <time> --to <time> [--request-id id] [--limit n] [--db path]
  api-proxy logs show <request-id> [--db path]

时间格式: RFC3339 / 2006-01-02T15:04:05 / 2006-01-02
日期-only 的 --from 取当天 00:00:00，--to 取当天 23:59:59.999
`

func RunLogs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", strings.TrimSpace(logsUsage))
	}
	switch args[0] {
	case "query":
		return runLogsQuery(args[1:])
	case "show":
		return runLogsShow(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, logsUsage)
		return nil
	default:
		return fmt.Errorf("未知 logs 子命令 %q\n%s", args[0], logsUsage)
	}
}

func runLogsQuery(args []string) error {
	flags := flag.NewFlagSet("logs query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fromRaw := flags.String("from", "", "起始时间")
	toRaw := flags.String("to", "", "结束时间")
	requestID := flags.String("request-id", "", "只看该 requestId")
	limit := flags.Int("limit", 0, "最多返回条数，0 表示不截断")
	dbPath := flags.String("db", "", "日志数据库路径")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("logs query 参数错误: %w\n%s", err, logsUsage)
	}
	if strings.TrimSpace(*fromRaw) == "" || strings.TrimSpace(*toRaw) == "" {
		return fmt.Errorf("logs query 需要 --from 与 --to\n%s", logsUsage)
	}
	fromTime, err := parseQueryTime(*fromRaw, false)
	if err != nil {
		return fmt.Errorf("解析 --from 失败: %w", err)
	}
	toTime, err := parseQueryTime(*toRaw, true)
	if err != nil {
		return fmt.Errorf("解析 --to 失败: %w", err)
	}
	if toTime.Before(fromTime) {
		return fmt.Errorf("--to 不能早于 --from")
	}

	store, err := openLogsStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	opts := logger.QueryOptions{
		From:      fromTime.UnixMilli(),
		To:        toTime.UnixMilli(),
		RequestID: strings.TrimSpace(*requestID),
		Limit:     *limit,
		Ascending: true,
	}
	ctx := context.Background()
	var appLogs []logger.AppLog
	if opts.RequestID == "" {
		appLogs, err = store.QueryAppLogs(ctx, opts)
		if err != nil {
			return err
		}
	}
	trafficLogs, err := store.QueryTrafficLogs(ctx, opts)
	if err != nil {
		return err
	}
	requestLogs, err := store.QueryRequestLogs(ctx, opts)
	if err != nil {
		return err
	}
	if appLogs == nil {
		appLogs = []logger.AppLog{}
	}
	if trafficLogs == nil {
		trafficLogs = []logger.TrafficLog{}
	}
	if requestLogs == nil {
		requestLogs = []json.RawMessage{}
	}
	return encodeJSON(logger.QueryResult{
		From:        fromTime.Format(time.RFC3339Nano),
		To:          toTime.Format(time.RFC3339Nano),
		AppLogs:     appLogs,
		TrafficLogs: trafficLogs,
		RequestLogs: requestLogs,
	})
}

func runLogsShow(args []string) error {
	flags := flag.NewFlagSet("logs show", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", "", "日志数据库路径")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("logs show 参数错误: %w\n%s", err, logsUsage)
	}
	requestID := strings.TrimSpace(flags.Arg(0))
	if requestID == "" {
		return fmt.Errorf("logs show 需要 request-id\n%s", logsUsage)
	}
	store, err := openLogsStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	opts := logger.QueryOptions{
		From:      0,
		To:        time.Now().UnixMilli(),
		RequestID: requestID,
		Ascending: true,
	}
	ctx := context.Background()
	trafficLogs, err := store.QueryTrafficLogs(ctx, opts)
	if err != nil {
		return err
	}
	requestLogs, err := store.QueryRequestLogs(ctx, opts)
	if err != nil {
		return err
	}
	if trafficLogs == nil {
		trafficLogs = []logger.TrafficLog{}
	}
	if requestLogs == nil {
		requestLogs = []json.RawMessage{}
	}
	return encodeJSON(logger.ShowResult{
		RequestID:   requestID,
		RequestLogs: requestLogs,
		TrafficLogs: trafficLogs,
	})
}

func openLogsStore(explicitPath string) (*logger.Store, error) {
	path := strings.TrimSpace(explicitPath)
	if path == "" {
		path = strings.TrimSpace(config.NewEnvConfig().LogDBPath)
	}
	if path == "" {
		path = logger.DefaultDBPath
	}
	return logger.OpenReadOnly(path)
}

func parseQueryTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000000000",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	var parseErr error
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, raw, time.Local)
		if err != nil {
			parseErr = err
			continue
		}
		if layout == "2006-01-02" && endOfDay {
			parsed = parsed.Add(24*time.Hour - time.Nanosecond)
		}
		return parsed, nil
	}
	if parseErr == nil {
		parseErr = fmt.Errorf("无法解析时间 %q", raw)
	}
	return time.Time{}, parseErr
}

func encodeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
