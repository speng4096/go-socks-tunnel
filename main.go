package main

import (
	"flag"
	"github.com/BurntSushi/toml"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"strings"
)

var logger *zap.Logger
var configFile = flag.String("config", "config.toml", "配置文件")
var config = struct {
	Logger struct {
		Filename   string
		Level      string
		MaxSize    int
		MaxAge     int
		MaxBackups int
	}
	Server struct {
		Bind    string
		MaxConn int
	}
	Pools []struct {
	}
	Provider []struct {
	}
}{}

func main() {
	// 配置文件解析
	if flag.Parse(); *configFile == "" {
		log.Panic("请提供'config'参数")
	}
	if _, err := toml.DecodeFile(*configFile, &config); err != nil {
		log.Panicf("读取配置文件失败: %s", err)
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

	// 启动服务
	go MustRunSocksServer()
	<-make(chan os.Signal, 1)
}
