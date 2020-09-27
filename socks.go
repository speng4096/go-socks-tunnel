package main

import (
	"go.uber.org/zap"
	"log"
	"net"
)

var connCh = make(chan net.Conn, 1024)

func RunHandleSocksClient(index int) {
	for conn := range connCh {
		logger.Info("连接进入", zap.Int("index", index), zap.String("remote", conn.RemoteAddr().String()))

		for {

		}
	}
}

func MustRunSocksServer() {
	l, err := net.Listen("tcp", config.Server.Bind)
	if err != nil {
		log.Panicf("监听socks端口失败: %s", err)
	}
	// 使用固定协程数处理连接
	for i := 0; i < config.Server.MaxConn; i++ {
		go RunHandleSocksClient(i)
	}

	// 将新连接放入队列
	for {
		client, err := l.Accept()
		if err != nil {
			logger.Warn("获取客户端连接失败", zap.Error(err))
			continue
		}
		connCh <- client
	}
}
