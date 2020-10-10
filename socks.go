package main

import "C"
import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"io"
	"log"
	"net"
	"time"
)

type ConnectionStatus int8

const (
	ConnectionStatusAccept  = iota // 等待客户端发出认证方法列表，随后回复选择的认证方法
	ConnectionStatusAuth           // 等待客户端发出认证帐号密码，随后回复认证结果
	ConnectionStatusCommand        // 等待客户端发出连接命令，随后回复连接结果(实际为连接代理服务器)
	ConnectionStatusFlow           // 等待客户端发出请求包，随后回复响应包
	ConnectionStatusFinish         // 连接将被关闭
)

type Connection struct {
	ClientTCP net.Conn
	ServerTCP net.Conn
	Status    ConnectionStatus
	Context   context.Context
}

func HandleConnection(conn *Connection, c *ServerConfig) error {
	// 避免连接处理异常造成崩溃
	defer func() {
		if r := recover(); r != nil {
			logger.Info("连接处理异常", zap.String("remote", conn.ClientTCP.RemoteAddr().String()))
		}
	}()
	//
	for {
		var response []byte
		var err error
		var n int
		var buffer [2048]byte

		for {
			// 读取隧道数据
			if conn.Status == ConnectionStatusFlow {
				// TODO: 对接ctx
				logger.Info("开始传输数据", zap.String("remote", conn.ClientTCP.RemoteAddr().String()))
				deadline := time.Now().Add(c.FlowTimeout * time.Millisecond)
				conn.ClientTCP.SetReadDeadline(deadline)
				conn.ServerTCP.SetReadDeadline(deadline)
				go io.Copy(conn.ClientTCP, conn.ServerTCP)
				io.Copy(conn.ServerTCP, conn.ClientTCP)
				logger.Info("结束传输数据", zap.String("remote", conn.ClientTCP.RemoteAddr().String()))
				return nil
			}
			// 读取握手数据
			conn.ClientTCP.SetReadDeadline(time.Now().Add(c.TCPClientReadTimeout * time.Millisecond))
			n, err = conn.ClientTCP.Read(buffer[:])
			if err != nil {
				return errors.New("读取数据失败")
			}
			logger.Debug("收到请求包", zap.Any("status", conn.Status), zap.ByteString("req", buffer[:n]))

			// 根据状态处理握手数据
			switch conn.Status {
			case ConnectionStatusAccept:
				if n < 3 {
					return errors.New("初始阶段数据异常")
				}
				if buffer[0] != 5 {
					return errors.New("仅支持Socks5协议")
				}
				// 客户端支持的认证方式
				// [0]     => NO AUTHENTICATION REQUIRED = 0x00
				// [1]     => USERNAME/PASSWORD          = 0x02
				// default => NO ACCEPTABLE METHODS      = 0xff
				var methods = [2]bool{}
				for i := byte(2); i < buffer[1]+2; i++ {
					switch buffer[i] {
					case 0x00:
						methods[0] = true
					case 0x02:
						methods[1] = true
					}
				}
				// 若配置了账号密码，仅支持0x02方式（帐号密码认证）
				// 若未配置账号密码，仅支持0x00方式（无认证）
				var method byte = 0xff
				if c.User != "" && c.Password != "" {
					if methods[1] {
						method = 0x02
						conn.Status = ConnectionStatusAuth
					}
				} else {
					if methods[0] {
						method = 0x00
						conn.Status = ConnectionStatusCommand
					}
				}
				if method == 0xff {
					return errors.New("无支持的认证方式")
				}
				response = []byte{0x05, method}
			case ConnectionStatusAuth:
				if n < 5 {
					return errors.New("认证阶段数据异常")
				}
				version := buffer[0]
				if version != 0x01 {
					return errors.Errorf("未支持的认证方式: %d", version)
				}
				ulen := buffer[1]
				user := buffer[2 : 2+ulen]
				plen := buffer[2+ulen]
				password := buffer[3+ulen : 3+ulen+plen]
				if string(user) == c.User && string(password) == c.Password {
					response = []byte{0x01, 0x00}
					logger.Info("客户已通过账密认证", zap.String("remote", conn.ClientTCP.RemoteAddr().String()), zap.ByteString("user", user))
					conn.Status = ConnectionStatusCommand
				} else {
					response = []byte{0x01, 0x01}
					logger.Info("客户账密错误", zap.String("remote", conn.ClientTCP.RemoteAddr().String()), zap.ByteString("user", user), zap.ByteString("password", password))
					conn.Status = ConnectionStatusFinish
				}
			case ConnectionStatusCommand:
				if n < 7 {
					return errors.New("命令阶段数据异常")
				}
				version := buffer[0]
				if version != 0x05 {
					return errors.New("仅支持Socks5协议")
				}
				// 命令类型
				// 目前仅支持CONNECT（0x01）类型
				command := buffer[1]
				if command != 0x01 {
					conn.Status = ConnectionStatusFinish
					response = []byte{0x05, 0x07, 0x00} // Command not supported
					goto Response
				}
				// 请求类型
				var ip net.IP
				var port uint16
				switch buffer[3] {
				case 0x01: // IPv4
					if n != 10 {
						return errors.New("命令阶段数据异常")
					}
					ip = net.IPv4(buffer[4], buffer[5], buffer[6], buffer[7])
					port = binary.BigEndian.Uint16(buffer[8:10])
				case 0x03: // Domain
					right := n - 2
					if right < 4 {
						return errors.New("命令阶段数据异常")
					}
					domain := string(buffer[5:right])
					ips, err := net.LookupIP(domain)
					if err != nil || len(ips) == 0 {
						conn.Status = ConnectionStatusFinish
						response = []byte{0x05, 0x04, 0x00} // Host unreachable
						goto Response
					}
					ip = ips[0]
					port = binary.BigEndian.Uint16(buffer[n-2 : n])
				default:
					conn.Status = ConnectionStatusFinish
					response = []byte{0x05, 0x08, 0x00} // Address type not supported
					goto Response
				}
				// TODO: 获取可用代理，目前本机直连服务器
				// 建立连接
				address := fmt.Sprintf("%s:%d", ip.String(), port)
				conn.ServerTCP, err = net.DialTimeout("tcp", address, time.Second)
				if err != nil {
					conn.Status = ConnectionStatusFinish
					response = []byte{0x05, 0x03, 0x00} // Network unreachable
				} else {
					conn.Status = ConnectionStatusFlow
					response = []byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // succeeded
				}
			case ConnectionStatusFinish:
				response = nil
				goto Response
			default:
				return errors.Errorf("连接状态异常: %d", conn.Status)
			}
			// 发出响应包
		Response:
			if response != nil {
				conn.ClientTCP.SetWriteDeadline(time.Now().Add(c.TCPClientWriteTimeout * time.Millisecond))
				_, err = conn.ClientTCP.Write(response)
				response = nil
				if err != nil {
					return errors.Wrap(err, "发送响应包失败")
				}
				logger.Debug("发送响应包", zap.ByteString("response", response))
			}
			if conn.Status == ConnectionStatusFinish {
				return nil
			}
		}
	}
}

func RunHandleConnection(ch chan *Connection, c ServerConfig) {
	for conn := range ch {
		logger.Info("开始处理连接", zap.String("remote", conn.ClientTCP.RemoteAddr().String()))
		HandleConnection(conn, &c)
		conn.ClientTCP.Close()
		if conn.ServerTCP != nil {
			conn.ServerTCP.Close()
		}
		logger.Info("结束处理连接", zap.String("remote", conn.ClientTCP.RemoteAddr().String()))
	}
}

func MustRunSocksServer() {
	for _, c := range config.Server {
		// 监听端口
		address, err := net.ResolveTCPAddr("tcp", c.Bind)
		if err != nil {
			log.Panicf("监听地址解析失败: %s, %s", c.Bind, err)
		}
		l, err := net.ListenTCP("tcp", address)
		if err != nil {
			log.Panicf("监听TCP端口失败: %s, %s", c.Bind, err)
		}

		// 启动处理协程
		ch := make(chan *Connection, 0)
		for i := 0; i < c.MaxConnections; i++ {
			go RunHandleConnection(ch, c)
		}

		// 将新连接放入处理队列
		go func() {
			for {
				client, err := l.AcceptTCP()
				logger.Info("客户端已连接", zap.String("remote", client.RemoteAddr().String()))
				if err != nil {
					logger.Warn("获取客户端连接失败", zap.Error(err))
					continue
				}
				ctx, _ := context.WithTimeout(context.Background(), c.FlowTimeout*time.Millisecond)
				conn := Connection{
					ClientTCP: client,
					Status:    ConnectionStatusAccept,
					Context:   ctx,
				}
				timer := time.NewTimer(c.WaitForHandleTimeout * time.Millisecond)
				select {
				case ch <- &conn:
				case <-timer.C:
					client.Close()
					logger.Warn("连接等待处理超时，已被关闭", zap.String("remote", client.RemoteAddr().String()))
				}
				timer.Stop()
			}
		}()

		logger.Info("Socks服务器已启动", zap.String("bind", c.Bind))
	}
}
