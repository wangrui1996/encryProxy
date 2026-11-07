package proxy

import (
    "time"
	"net"
	"sync/atomic"
    com "wangjui.com/enproxy/common"
)

type Traffic struct {
	listen string
	currentSecUploadBytes   int64
	currentSecDownloadBytes int64
}

func NewTraffic(listen string) *Traffic {
	return &Traffic{
		listen: listen,
	}
}

type TrafficConn struct {
    net.Conn
    traffic *Traffic
}

func NewTrafficConn(conn net.Conn, traffic *Traffic) *TrafficConn {
	return &TrafficConn{
		Conn:    conn,
		traffic: traffic,
	}
}

// 统计下游接收的字节。
func (c *TrafficConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)

	if n > 0 {
		atomic.AddInt64(&c.traffic.currentSecUploadBytes, int64(n))
	}

	return n, err
}

func (c *TrafficConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)

	if n > 0 {
		atomic.AddInt64(&c.traffic.currentSecDownloadBytes, int64(n))
	}

	return n, err
}

func (t *Traffic) Start() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		upBytes := atomic.SwapInt64(&t.currentSecUploadBytes, 0)
		downBytes := atomic.SwapInt64(&t.currentSecDownloadBytes, 0)
		if upBytes > 0 || downBytes > 0 {
			com.Log.Infof(
				"[Traffic Stat] Port: %s | Upload: %.2f KB/s | Download: %.2f KB/s",
				t.listen,
				float64(upBytes)/1024,
				float64(downBytes)/1024,
			)
		}
	}
}