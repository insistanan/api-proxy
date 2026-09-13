package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

var defaultStore atomic.Pointer[Store]

// Config 日志系统配置。正文与运行日志一律进 sqlite，不再写 logs/ 文件。
type Config struct {
	DBPath  string
	Console bool
}

func DefaultConfig() *Config {
	return &Config{
		DBPath:  DefaultDBPath,
		Console: false,
	}
}

func Default() *Store {
	return defaultStore.Load()
}

// Setup 初始化 sqlite 日志库，并把标准库 log / Gin 输出接到该库。
func Setup(cfg *Config) (*Store, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	dbPath := strings.TrimSpace(cfg.DBPath)
	if dbPath == "" {
		dbPath = DefaultDBPath
	}
	store, err := Open(dbPath)
	if err != nil {
		return nil, err
	}

	var writer io.Writer = newLineWriter(store)
	if cfg.Console {
		writer = io.MultiWriter(os.Stdout, writer)
	}
	log.SetOutput(writer)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	gin.DefaultWriter = writer
	gin.DefaultErrorWriter = writer
	defaultStore.Store(store)

	log.Printf("[Logger-Init] 日志系统已初始化: %s（保留今天与昨天）", dbPath)
	return store, nil
}

func CloseDefault() error {
	store := defaultStore.Swap(nil)
	if store == nil {
		return nil
	}
	err := store.Close()
	if err != nil {
		return fmt.Errorf("关闭日志存储失败: %w", err)
	}
	return nil
}
