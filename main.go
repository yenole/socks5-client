package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var socks = flag.String("socks", "127.0.0.1:1080", "socks address")

func main() {
	flag.Parse()

	var wg sync.WaitGroup
	args := flag.CommandLine.Args()
	if len(args) == 0 {
		goto help
	}

	for _, v := range args {
		if strings.Count(v, ":") == 2 {
			wg.Add(1)
			list := strings.Split(v, ":")
			go createServer(&wg, list[0], strings.Join(list[1:], ":"))
		}
	}
	wg.Wait()
	os.Exit(0)

help:
	flag.PrintDefaults()
}

func createServer(wg *sync.WaitGroup, listen, address string) {
	defer wg.Done()

	var l net.Listener
	var err error

	l, err = net.Listen("tcp", ":"+listen)
	if err != nil {
		fmt.Println("Error listening:", err)
		os.Exit(1)
	}
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting: ", err)
			os.Exit(1)
		}
		fmt.Printf("<<-- %s\n", conn.RemoteAddr())
		go handleServerConn(conn, address)
	}
}

func handleServerConn(localConn net.Conn, address string) {
	defer localConn.Close()

	// 连接远端
	remoteConn, err := net.Dial("tcp", *socks)
	if err != nil {
		fmt.Println("远程连接错误:", err.Error())
		return
	}
	defer remoteConn.Close()

	// 发送socks握手信息
	_, err = remoteConn.Write([]byte{05, 01, 00})
	if err != nil {
		fmt.Println("发送握手失败:", err.Error())
		return
	}

	// 接收socks握手信息
	buf := make([]byte, 2)
	len, err := remoteConn.Read(buf)
	if err != nil {
		fmt.Println("接收握手失败:", err.Error())
		return
	}
	if bytes.Compare(buf[:len], []byte{05, 00}) != 0 {
		fmt.Println("非标准的socks5握手")
		return
	}

	// 发送需要连接的地址
	addrBytes := []byte{05, 01, 00, 01}
	list := strings.Split(address, ":")
	ipBytes := Int32ToBytes(StringIpToInt(list[0]))
	mysqlPortNumber, _ := strconv.Atoi(list[1])
	portBytes := Int16ToBytes(mysqlPortNumber)
	addrBytes = append(addrBytes, ipBytes...)
	addrBytes = append(addrBytes, portBytes...)
	_, err = remoteConn.Write(addrBytes)
	if err != nil {
		fmt.Println("发送需要连接的地址失败:", err.Error())
		return
	}

	// 接收socks服务端的远程连接结果
	buf = make([]byte, 1024)
	len, err = remoteConn.Read(buf)
	if err != nil {
		fmt.Println("socks连接数据库失败:", err.Error())
		return
	}
	if bytes.Compare(buf[:2], []byte{05, 00}) != 0 {
		fmt.Println("socks连接数据库失败")
		return
	}
	if len > 10 {
		_, err := localConn.Write(buf[10:len])
		if err != nil {
			fmt.Println("第一次发包失败:", err.Error())
			return
		}
	}

	var isFinished = false
	wg := sync.WaitGroup{}
	wg.Add(2)
	go handleBindCon(remoteConn, localConn, &wg, &isFinished)
	go handleBindCon(localConn, remoteConn, &wg, &isFinished)

	wg.Wait()
}

func handleBindCon(con1 net.Conn, con2 net.Conn, wg *sync.WaitGroup, isFinished *bool) {
	defer wg.Done()
	defer func() { *isFinished = true }()
	for {
		buf := make([]byte, 1024)
		err := con1.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err != nil {
			fmt.Println("con1 setDeadLine failed:", err.Error())
			if *isFinished == true {
				return
			}
			continue
		}
		len, err := con1.Read(buf)
		if err != nil {
			if oe, ok := err.(*net.OpError); ok {
				isTimeout := oe.Timeout()
				if isTimeout {
					if *isFinished == true {
						return
					}
					//fmt.Println("read超时等待:", err.Error())
					continue
				}
			}
			if err.Error() == "EOF" {
				return
			}
			fmt.Println("收包错误:", err.Error())
			return
		}

		_, err = con2.Write(buf[:len])
		if err != nil {
			fmt.Println("发包错误:", err.Error())
			return
		}
	}
}
