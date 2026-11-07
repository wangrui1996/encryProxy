package com

import (
	"fmt"
	"io"
	"os"
	"github.com/sirupsen/logrus"
)

var Log = logrus.New()

type CustomFormatter struct{}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// 格式化时间
	timestamp := entry.Time.Format("2006-01-02 15:04:05")
	// 日志级别转大写 (INFO, ERROR, WARNING 等)
	level := entry.Level.String()
	// 拼装成你想要的格式: 时间 [级别] 消息
	logLine := fmt.Sprintf("%s [%s] %s\n", timestamp, level, entry.Message)
	return []byte(logLine), nil
}

// InitLogger 初始化日志
// filePath: 日志文件路径。如果为空，则只输出到终端
// levelStr: 日志级别 ("debug", "info", "warn", "error")。如果为空，默认 "info"
func InitLogger(filePath string, levelStr string) {
	// 1. 设置日志级别
	level := logrus.InfoLevel // 默认 info
	if levelStr != "" {
		parsedLevel, err := logrus.ParseLevel(levelStr)
		if err == nil {
			level = parsedLevel
		}
	}
	Log.SetLevel(level)

	Log.SetFormatter(&CustomFormatter{})

	var writers []io.Writer
	writers = append(writers, os.Stdout) // 默认一定要在终端打印
	outputStatus := "控制台 (Stdout)"
	if filePath != "" {
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err == nil {
			writers = append(writers, file) // 如果文件打开成功，加入写入列表
			outputStatus = fmt.Sprintf("控制台 (Stdout) & 文件 (%s)", filePath)
		}
	}

	Log.SetOutput(io.MultiWriter(writers...))

	fmt.Printf("日志初始化成功 | 级别: [%s] | 输出位置: [%s]", level.String(), outputStatus)
}