package main

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"time"
)

var encoderConfig = zapcore.EncoderConfig{
	MessageKey:    "message",
	LevelKey:      "level",
	TimeKey:       "time",
	NameKey:       "name",
	CallerKey:     "caller",
	StacktraceKey: "stack",
	LineEnding:    zapcore.DefaultLineEnding,
	EncodeLevel:   zapcore.LowercaseLevelEncoder,
	EncodeTime: func(time time.Time, encoder zapcore.PrimitiveArrayEncoder) {
		encoder.AppendString(time.Format("2006-01-02 15:04:05"))
	},
	EncodeDuration: zapcore.SecondsDurationEncoder,
	EncodeCaller:   zapcore.ShortCallerEncoder,
}

func NewRotateFileLogger(filename string, maxSize, maxAge, maxBackups int, minLevel zapcore.Level) *zap.Logger {
	syncer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,
		MaxAge:     maxAge,
		MaxBackups: maxBackups,
		LocalTime:  false, // 使用UTC时间
		Compress:   false, // 不启用gzip压缩
	})
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), syncer, minLevel)
	return zap.New(core, zap.AddCaller())
}

// 新建Logger，日志输出到控制台（标准输出）
func NewConsoleLogger(minLevel zapcore.Level) *zap.Logger {
	syncer := zapcore.AddSync(os.Stdout)
	core := zapcore.NewCore(zapcore.NewConsoleEncoder(encoderConfig), syncer, minLevel)
	return zap.New(core, zap.AddCaller())
}
