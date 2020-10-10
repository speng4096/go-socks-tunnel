package main

import (
	"encoding/binary"
	"fmt"
	"go.uber.org/zap"
	"io"
	"log"
	"net"
	"time"
)

const (
	connStatusInit    = iota // 等待客户端发出认证方法列表，随后回复选择的认证方法
	connStatusAuth           // 等待客户端发出认证帐号密码，随后回复认证结果
	connStatusCommand        // 等待客户端发出连接命令，随后回复连接结果(实际为连接代理服务器)
	connStatusTran           // 等待客户端发出请求包，随后回复响应包
)

func RunHandleSocksClient(index int, ch chan *net.TCPConn, server ServerConfig) {
	for conn := range ch {
		var status = connStatusInit
		var buffer [1024]byte
		var response []byte
		var isCloseConn = false
		var client net.Conn
		var n int
		var err error
		logger.Info("客户进入", zap.Int("index", index), zap.String("remote", conn.RemoteAddr().String()))
	HandleFor:
		for {
			if client == nil {
				// 读取控制数据
				conn.SetReadDeadline(time.Now().Add(time.Millisecond * time.Duration(server.TCPReadTimeout)))
				n, err = conn.Read(buffer[:])
				if err != nil {
					break HandleFor
				}
				logger.Info("收到控制包", zap.Int("status", status), zap.ByteString("req", buffer[:n]))
			} else {
				// 传输流量
				logger.Info("开始传输数据流")
				go io.Copy(conn, client)
				m, err := io.Copy(client, conn)
				logger.Info("结束传输数据流", zap.Int64("m", m), zap.Error(err))
			}

		StatusSwitch:
			switch status {
			case connStatusInit:
				if n < 3 {
					conn.Close()
					return
				}
				// 仅支持Socks5协议
				if buffer[0] != 5 {
					conn.Close()
					return
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
				logger.Info("客户支持的认证方式", zap.Bools("methods", methods[:]))
				// 若配置了账号密码，仅支持0x02方式（帐号密码认证）
				// 若未配置账号密码，仅支持0x00方式（无认证）
				var method byte = 0xff
				if server.User != "" && server.Password != "" {
					if methods[1] {
						method = 0x02
						status = connStatusAuth
					}
				} else {
					if methods[0] {
						method = 0x00
						status = connStatusCommand
					}
				}
				if method == 0xff {
					isCloseConn = true
				}
				response = []byte{0x05, method}
				logger.Info("客户完成『初始化』阶段", zap.String("remote", conn.RemoteAddr().String()))
			case connStatusAuth:
				if n < 5 {
					conn.Close()
					return
				}
				version := buffer[0]
				if version != 0x01 {
					break HandleFor
				}
				ulen := buffer[1]
				user := buffer[2 : 2+ulen]
				plen := buffer[2+ulen]
				password := buffer[3+ulen : 3+ulen+plen]
				if string(user) == server.User && string(password) == server.Password {
					response = []byte{0x01, 0x00}
					logger.Info("客户完成『认证』阶段", zap.String("remote", conn.RemoteAddr().String()))
					status = connStatusCommand
				} else {
					response = []byte{0x01, 0x01}
					logger.Info("客户密码错误", zap.String("remote", conn.RemoteAddr().String()), zap.ByteString("user", user), zap.ByteString("password", password))
					isCloseConn = true
				}
			case connStatusCommand:
				if n < 7 {
					conn.Close()
					return
				}
				version := buffer[0]
				if version != 0x05 {
					conn.Close()
					return
				}
				// 命令类型
				// 目前仅支持CONNECT（0x01）类型
				command := buffer[1]
				if command != 0x01 {
					isCloseConn = true
					response = []byte{0x05, 0x07, 0x00} // Command not supported
					break StatusSwitch
				}
				// 请求类型
				var ip net.IP
				var port uint16
				switch buffer[3] {
				case 0x01: // IPv4
					if n != 10 {
						conn.Close()
						return
					}
					ip = net.IPv4(buffer[4], buffer[5], buffer[6], buffer[7])
					port = binary.BigEndian.Uint16(buffer[8:10])
				case 0x03: // Domain
					right := n - 2
					if right < 4 {
						conn.Close()
						return
					}
					domain := string(buffer[5:right])
					ips, err := net.LookupIP(domain)
					if err != nil || len(ips) == 0 {
						isCloseConn = true
						response = []byte{0x05, 0x04, 0x00} // Host unreachable
						break StatusSwitch
					}
					ip = ips[0]
					port = binary.BigEndian.Uint16(buffer[n-2 : n])
				default:
					isCloseConn = true
					response = []byte{0x05, 0x08, 0x00} // Address type not supported
					break StatusSwitch
				}
				// 建立连接
				address := fmt.Sprintf("%s:%d", ip.String(), port)
				client, err = net.DialTimeout("tcp", address, time.Duration(server.TCPDialTimeout)*time.Millisecond)
				if err != nil {
					response = []byte{0x05, 0x03, 0x00} // Network unreachable
					isCloseConn = true
					break
				}
				response = []byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // succeeded
			default:
				logger.Info("客户状态异常", zap.String("remote", conn.RemoteAddr().String()))
				break HandleFor
			}
			// 发出响应包
			if response != nil {
				logger.Info("响应", zap.ByteString("response", response))
				conn.SetWriteDeadline(time.Now().Add(time.Millisecond * time.Duration(server.TCPWriteTimeout)))
				_, err = conn.Write(response)
				response = nil
				if err != nil {
					break HandleFor
				}
			}
			if isCloseConn {
				break HandleFor
			}
		}
		logger.Info("未能建立连接", zap.String("remote", conn.RemoteAddr().String()))
		conn.Close()
	}
}

func MustRunSocksServer() {
	for _, server := range config.Server {
		addr, err := net.ResolveTCPAddr("tcp", server.Bind)
		if err != nil {
			log.Panicf("监听地址解析失败: %s, %s", server.Bind, err)
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			log.Panicf("监听TCP端口失败: %s, %s", server.Bind, err)
		}
		// 使用固定协程数处理连接
		ch := make(chan *net.TCPConn, 1024)
		for i := 0; i < server.MaxConn; i++ {
			go RunHandleSocksClient(i, ch, server)
		}

		// 将新连接放入队列
		go func() {
			for {
				client, err := l.AcceptTCP()
				if err != nil {
					logger.Warn("获取客户端连接失败", zap.Error(err))
					continue
				}
				ch <- client
			}
		}()
	}
}
