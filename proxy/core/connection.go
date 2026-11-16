package core

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"wangjui.com/enproxy/proxy/stat"
)

func (p *Proxy) handleConnection(inboundConn net.Conn) {
	accountName := "anonymous"
	var account *Account
	if p.inbound.Crypto != nil && p.inbound.Crypto.AuthRequired {
		var err error
		account, err = p.authenticate(inboundConn)
		if err != nil {
			Logger.Warnf("client %s authentication failed on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
			return
		}
		accountName = account.Name
	}

	// 统计原始入站线缆字节；TCP 原有包装顺序不变。
	var clientConn net.Conn = NewRateLimitedConn(inboundConn, p.portRateLimit)
	if account != nil && account.RateLimit != nil {
		clientConn = NewRateLimitedConn(clientConn, account.RateLimit)
	}
	// HTTP Basic 账号在收到第一个请求后才能确定，允许切换后续统计与限速。
	accountLimiter := &accountRateConn{Conn: clientConn}
	clientConn = accountLimiter
	trafficConn := stat.NewTrafficConn(clientConn, p.traffic, p.trafficMgr.RegisterUser(accountName))
	clientConn = trafficConn

	var err error
	clientConn, err = wrapCrypto(clientConn, p.inbound.Crypto)
	if err != nil {
		Logger.Errorf("create inbound encryption stream: %v", err)
		return
	}
	// The protocol decides when credentials are needed; core only applies account policy.
	proxyAuthenticate := func(username, password string) error {
		if p.authenticator == nil {
			return fmt.Errorf("proxy authenticator is nil")
		}
		acc, err := p.authenticator.AuthenticateProxy(username, password)
		if err != nil {
			return err
		}
		if acc == nil {
			return fmt.Errorf("proxy authenticator returned nil account")
		}
		accountLimiter.SetLimit(acc.RateLimit)
		trafficConn.SetUserTraffic(p.trafficMgr.RegisterUser(acc.Name))
		return nil
	}

	if err := p.handler.Handle(context.Background(), clientConn, proxyAuthenticate); err != nil {
		Logger.Errorf("client %s on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
	}
}

// accountRateConn can switch to a newly authenticated proxy account while
// retaining the same TCP stream and sharing the account's existing limiters.
type accountRateConn struct {
	net.Conn
	current atomic.Pointer[RateLimit]
}

func (c *accountRateConn) SetLimit(limit *RateLimit) { c.current.Store(limit) }

func (c *accountRateConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		if lim := c.current.Load(); lim != nil {
			if waitErr := waitN(lim.uploadLimiter, n); waitErr != nil {
				return n, waitErr
			}
		}
	}
	return n, err
}

func (c *accountRateConn) Write(b []byte) (int, error) {
	if lim := c.current.Load(); lim != nil {
		if err := waitN(lim.downloadLimiter, len(b)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}

func (c *accountRateConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}
