package stat

import "net"

type TrafficConn struct {
    net.Conn

    portTraffic *Traffic
    userTraffic *Traffic
}

func NewTrafficConn(
    conn net.Conn,
    portTraffic *Traffic,
    userTraffic *Traffic,
) *TrafficConn {
    return &TrafficConn{
        Conn:        conn,
        portTraffic: portTraffic,
        userTraffic: userTraffic,
    }
}

// Read: 下游 -> 代理，计为上传。
func (c *TrafficConn) Read(b []byte) (int, error) {
    n, err := c.Conn.Read(b)

    if n > 0 {
        addTraffic(c.portTraffic, int64(n), 0)
        addTraffic(c.userTraffic, int64(n), 0)
    }

    return n, err
}

// Write: 代理 -> 下游，计为下载。
func (c *TrafficConn) Write(b []byte) (int, error) {
    n, err := c.Conn.Write(b)

    if n > 0 {
        addTraffic(c.portTraffic, 0, int64(n))
        addTraffic(c.userTraffic, 0, int64(n))
    }

    return n, err
}

func (c *TrafficConn) CloseWrite() error {
    cw, ok := c.Conn.(interface {
        CloseWrite() error
    })
    if !ok {
        return net.ErrClosed
    }

    return cw.CloseWrite()
}