package proxy

import (
	"bufio"
	"io"
	"context"
	"net"
	"sync"
	"time"
	"sync/atomic"
	"golang.org/x/time/rate"
	com "wangjui.com/enproxy/common"
)

type Config struct {
	Proxy ProxyConfig `json:"proxy"`
}

type RateLimitConfig struct {
	UploadKbps   int `json:"up_kbps"`   // 上行限速 KB/s
	DownloadKbps int `json:"down_kbps"` // 下行限速 KB/s
}

type ProxyConfig struct {
	Listen string `json:"listen"`
	Encrypt bool `json:"encrypt"`
	Decrypt bool `json:"decrypt"`
	Key string `json:"key"`
	RateLimit      RateLimitConfig `json:"rate_limit"`
	Upstream UpstreamConfig `json:"upstream"`
}

type UpstreamConfig struct {
	Name string `json:"name"`
	Url string `json:"url"`
	Encrypt bool `json:"encrypt"`
	Decrypt bool `json:"decrypt"`
	Key string `json:"key"`
}

type Proxy struct {
	config ProxyConfig
	uploadLimiter   *rate.Limiter // 全局上行限流器
	downloadLimiter *rate.Limiter
	currentSecUploadBytes   int64 // 当前秒累计上传字节数
	currentSecDownloadBytes int64 // 当前秒累计下载字节数
}

type RateLimitedConn struct {
	net.Conn
	proxy *Proxy
}

func (c *RateLimitedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		atomic.AddInt64(&c.proxy.currentSecUploadBytes, int64(n))

		if c.proxy.downloadLimiter != nil { // 注意：限流器本身的逻辑保持不变
			_ = c.proxy.downloadLimiter.WaitN(context.Background(), n)
		}
	}
	return n, err
}

func (c *RateLimitedConn) Write(b []byte) (int, error) {
	if len(b) > 0 {
		atomic.AddInt64(&c.proxy.currentSecDownloadBytes, int64(len(b)))

		if c.proxy.uploadLimiter != nil {
			_ = c.proxy.uploadLimiter.WaitN(context.Background(), len(b))
		}
	}
	return c.Conn.Write(b)
}



func NewProxy(cfg *Config) *Proxy {
    proxy := &Proxy{
        config: cfg.Proxy,
	}

    // 在创建 Proxy 时，初始化【全局/端口级别】限流器
    if cfg.Proxy.RateLimit.UploadKbps > 0 {
		limit := rate.Limit(cfg.Proxy.RateLimit.UploadKbps * 1024)
		// burst 设置大一点，允许突发流量平滑通过
		//proxy.uploadLimiter = rate.NewLimiter(limit, int(limit)*2)
		proxy.uploadLimiter = rate.NewLimiter(limit, int(limit))
		com.Log.Infof("Global Upload rate limit enabled: %d KB/s", cfg.Proxy.RateLimit.UploadKbps)
	}
	if cfg.Proxy.RateLimit.DownloadKbps > 0 {
		limit := rate.Limit(cfg.Proxy.RateLimit.DownloadKbps * 1024)
		//proxy.downloadLimiter = rate.NewLimiter(limit, int(limit)*2)
		proxy.downloadLimiter = rate.NewLimiter(limit, int(limit))
		com.Log.Infof("Global Download rate limit enabled: %d KB/s", cfg.Proxy.RateLimit.DownloadKbps)
	}

	go proxy.startTrafficMonitor()

	return proxy
}

func (proxy *Proxy) startTrafficMonitor() {
	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		// 取出并重置当前秒的字节数
		upBytes := atomic.SwapInt64(&proxy.currentSecUploadBytes, 0)
		downBytes := atomic.SwapInt64(&proxy.currentSecDownloadBytes, 0)

		// 如果这一秒有流量，或者你想每秒都打印，可以放开条件
		if upBytes > 0 || downBytes > 0 {
			upKB := float64(upBytes) / 1024.0
			downKB := float64(downBytes) / 1024.0
			com.Log.Infof("[Traffic Stat] Port: %s | Upload: %.2f KB/s | Download: %.2f KB/s",
				proxy.config.Listen, upKB, downKB)
		}
	}
}


func (proxy *Proxy) Run() {
	server, err := net.Listen("tcp", proxy.config.Listen)
	if err != nil {
		com.Log.Fatalf(err.Error())
	}
	com.Log.Infof("start to listen %s", proxy.config.Listen)

	for {
		s_conn, err := server.Accept()
		if err != nil {
			com.Log.Errorf("log failed: %s\n", err.Error())
			continue
		}
		com.Log.Infof("login IP %s success\n", s_conn.RemoteAddr())

		wrappedClientConn := &RateLimitedConn{
			Conn:  s_conn,
			proxy: proxy,
		}

		d_tcpAddr, _ := net.ResolveTCPAddr("tcp4", proxy.config.Upstream.Url)
		d_conn, err := net.DialTCP("tcp", nil, d_tcpAddr)
		if err != nil {
			com.Log.Errorf("log failed: %s\n", err.Error())
			s_conn.Close()
			continue
		}
		go func() {
			err := proxy.handleTCPClient(wrappedClientConn, d_conn)
			if err != nil {
				com.Log.Errorf("handle err: %s\n", err.Error())
			}
		}()
	}

}

func (proxy *Proxy) handleTCPClient(srcConn , dstConn net.Conn) error {
	var wait sync.WaitGroup
	wait.Add(2)
	go proxy.handleUpstream(srcConn, dstConn, &wait)
	go proxy.handleDownstream(srcConn, dstConn, &wait)
	wait.Wait()
	com.Log.Infof("IP %s login exit",srcConn.RemoteAddr())
	return nil
}

func (proxy *Proxy) handleUpstream(srcConn , dstConn net.Conn, wait *sync.WaitGroup) error {
	defer srcConn.Close()
	defer dstConn.Close()
	defer wait.Done()
	key := []byte(proxy.config.Upstream.Key)

	if proxy.config.Upstream.Encrypt {
		srcReader := bufio.NewReader(srcConn)
		for {
			readString, err := srcReader.ReadString('\n')
			if err != nil {
				return err
			}
			readString = readString[:len(readString)-1]
			encryptMsg, _ := encrypt(key, readString)

			dstConn.Write([]byte(encryptMsg))
			dstConn.Write([]byte("\n"))

		}

	} else if proxy.config.Upstream.Decrypt{
		srcReader := bufio.NewReader(srcConn)
		for {
			readString, err := srcReader.ReadString('\n')
			if err != nil {
				return err
			}
			readString = readString[:len(readString)-1]
			msg, _ := Decrypt(key, readString)
			dstConn.Write([]byte(msg))
			dstConn.Write([]byte("\n"))

		}
	} else {
		io.Copy(dstConn, srcConn)
	}
	return nil
}

func (proxy *Proxy) handleDownstream(srcConn , dstConn net.Conn, wait *sync.WaitGroup) error {
	defer srcConn.Close()
	defer dstConn.Close()
	defer wait.Done()

	key := []byte(proxy.config.Key)


	if proxy.config.Encrypt {
		dstReader := bufio.NewReader(dstConn)
		for {
			readBytes, err := dstReader.ReadBytes('\n')
			//fmt.Println(readBytes)
			readString := string(readBytes)
			//readString, err := dstReader.ReadString('\n')
			if err != nil {
				return err
			}
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]
			//fmt.Print("downstream %s", readString)
			//result, err := remoteReader.ReadString('\n')
			encryptMsg, _ := encrypt(key, readString)
			//readString = encryptMsg + "\n"
			srcConn.Write([]byte(encryptMsg))
			srcConn.Write([]byte("\n"))

		}

	} else if proxy.config.Decrypt {
		//log.Printf("start Decrypt DownStream")
		dstReader := bufio.NewReader(dstConn)
		for {
			readString, err := dstReader.ReadString('\n')
			if err != nil {
				//log.Printf(err.Error())
				return err
			}
			//log.Printf("%s\n", readString)
			//log.Printf("\nslen: %d\n", len(readString))
			readString = readString[:len(readString)-1]

			//result, err := remoteReader.ReadString('\n')
			msg, _ := Decrypt(key, readString)
			//fmt.Printf("downstream decrypt: %v|",msg)
			//fmt.Print(msg)
			//readString = msg + "\n"
			srcConn.Write([]byte(msg))
			srcConn.Write([]byte("\n"))

		}
	} else {
		io.Copy(srcConn, dstConn)
	}

	return nil
}
