package proxy

import (
	"fmt"
	"net"
	"sync"

	com "wangjui.com/enproxy/common"
)

type Proxy struct {
	inbound       InboundConfig
	accounts      *AccountManager
	routeManager  *RouteManager
	portRateLimit *RateLimit
	traffic       *Traffic
}

func NewProxy(inbound InboundConfig, accounts *AccountManager, routeManager *RouteManager, trafficManager *TrafficManager) *Proxy {
	return &Proxy{
		inbound:       inbound,
		accounts:      accounts,
		routeManager:  routeManager,
		portRateLimit: NewRateLimit(inbound.RateLimit),
		traffic:       trafficManager.Register(inbound.Listen),
	}
}

func (p *Proxy) handleConnection(inboundConn net.Conn) {
	defer inboundConn.Close()

	var account *Account
	if p.inbound.AuthRequired {
		var err error
		account, err = p.authenticate(inboundConn)
		if err != nil {
			com.Log.Errorf("client %s authentication failed on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
			return
		}
		com.Log.Debugf("client %s authenticated as %s", inboundConn.RemoteAddr(), account.Name)
	}

	outbound, err := p.routeManager.Resolve(p.inbound.Tag)
	if err != nil {
		com.Log.Errorf("resolve route for inbound %s failed: %v", p.inbound.Tag, err)
		return
	}

	outboundConn, err := net.Dial("tcp", outbound.Server)
	if err != nil {
		com.Log.Errorf("dial outbound %s (%s) failed: %v", outbound.Tag, outbound.Server, err)
		return
	}
	defer outboundConn.Close()

	// 如果配置了上游账号，先完成上游认证。
	if outbound.Account != nil {
		if err := authenticateOutbound(
			outboundConn,
			outbound.Account,
			outbound.Crypto,
		); err != nil {
			com.Log.Errorf(
				"authenticate outbound %s (%s) failed: %v",
				outbound.Tag,
				outbound.Server,
				err,
			)
			return
		}

		com.Log.Debugf(
			"authenticated to outbound %s as %s",
			outbound.Tag,
			outbound.Account.Username,
		)
	}

	// 认证发生在包装之前，因此认证帧不进入业务限速和流量统计。
	var clientConn net.Conn = inboundConn
	if p.portRateLimit != nil {
		clientConn = NewRateLimitedConn(clientConn, p.portRateLimit)
	}
	if account != nil && account.RateLimit != nil {
		clientConn = NewRateLimitedConn(clientConn, account.RateLimit)
	}
	clientConn = NewTrafficConn(clientConn, p.traffic)

	if err := p.handleTCPClient(clientConn, outboundConn, outbound); err != nil {
		com.Log.Errorf("handle client %s on %s failed: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
	}
}

func (p *Proxy) Run() {
	server, err := net.Listen("tcp", p.inbound.Listen)
	if err != nil {
		com.Log.Errorf("listen %s failed: %v", p.inbound.Listen, err)
		return
	}
	defer server.Close()

	com.Log.Infof("inbound %s listening on %s", p.inbound.Tag, p.inbound.Listen)
	for {
		inboundConn, err := server.Accept()
		if err != nil {
			com.Log.Errorf("accept on %s failed: %v", p.inbound.Listen, err)
			continue
		}
		com.Log.Debugf("client %s connected to %s", inboundConn.RemoteAddr(), p.inbound.Tag)
		go p.handleConnection(inboundConn)
	}
}

func closeWrite(conn net.Conn) {
	if conn, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = conn.CloseWrite()
		return
	}
	_ = conn.Close()
}

func (p *Proxy) handleTCPClient(inboundConn, outboundConn net.Conn, outbound *OutboundConfig) error {
	if outbound == nil {
		return fmt.Errorf("outbound is nil")
	}

	var wait sync.WaitGroup
	wait.Add(2)
	errCh := make(chan error, 2)

	go func() {
		defer wait.Done()
		err := p.handleUpstream(inboundConn, outboundConn, outbound)
		errCh <- err
		if err != nil {
			_ = inboundConn.Close()
			_ = outboundConn.Close()
			return
		}
		closeWrite(outboundConn)
	}()

	go func() {
		defer wait.Done()
		err := p.handleDownstream(outboundConn, inboundConn, outbound)
		errCh <- err
		if err != nil {
			_ = inboundConn.Close()
			_ = outboundConn.Close()
			return
		}
		closeWrite(inboundConn)
	}()

	wait.Wait()
	close(errCh)

	var firstErr error
	for err := range errCh {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
