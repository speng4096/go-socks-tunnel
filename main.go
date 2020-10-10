package main

import (
	"flag"
	"github.com/BurntSushi/toml"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

type ServerConfig struct {
	Bind                  string
	MaxConnections        int
	IPProviders           []string
	User                  string
	Password              string
	FlowTimeout           time.Duration // ms
	TCPClientReadTimeout  time.Duration // ms
	TCPClientWriteTimeout time.Duration // ms
	WaitForHandleTimeout  time.Duration // ms
}

type Config struct {
	Logger struct {
		Filename   string
		Level      string
		MaxSize    int
		MaxAge     int
		MaxBackups int
	}
	IPProvider []struct {
		Name string
		URL  string
	}
	Server []ServerConfig
}

var logger *zap.Logger
var configFile = flag.String("config", "config.toml", "配置文件")
var config = Config{}

func main() {
	// 配置
	if flag.Parse(); *configFile == "" {
		log.Panic("请使用'--config'指定配置文件")
	}
	if _, err := toml.DecodeFile(*configFile, &config); err != nil {
		log.Panicf("读取配置文件失败, %s, %s", *configFile, err)
	}

	// 日志
	level := zapcore.Level(0)
	if err := level.UnmarshalText([]byte(config.Logger.Level)); err != nil {
		log.Panicf("配置文件中'Logger.Level'字段有误: %s", err)
	}
	if strings.ToLower(config.Logger.Filename) == "console" {
		logger = NewConsoleLogger(level)
	} else {
		logger = NewRotateFileLogger(config.Logger.Filename, config.Logger.MaxSize, config.Logger.MaxAge, config.Logger.MaxBackups, level)
	}

	// 启动
	MustRunSocksServer()
	runtime.GC()
	<-make(chan os.Signal, 1)
}
