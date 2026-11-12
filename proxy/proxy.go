package proxy

import (
	"net"
	"sync"
	com "wangjui.com/enproxy/common"
)

type Proxy struct {
	config ProxyConfig
	rateLimit *RateLimit // 限速模块
	traffic *Traffic // 流量统计模块
}

func NewProxy(cfg ProxyConfig, trafficManager *TrafficManager) *Proxy {
	return &Proxy{
		config:    cfg,
		rateLimit: NewRateLimit(cfg.RateLimit),
		traffic:   trafficManager.Register(cfg.Listen),
	}
}

func (p *Proxy) handleConnection(inboundConn net.Conn, reqUrl string) {
	upstreamAddr, err := net.ResolveTCPAddr("tcp4", reqUrl)
	if err != nil {
		com.Log.Errorf("resolve upstream %s failed: %v", p.config.Upstream.Url, err)
		return
	}

	rateLimitedConn := NewRateLimitedConn(inboundConn, p.rateLimit)
	wrappedInboundConn := NewTrafficConn(rateLimitedConn, p.traffic)

	// 建立上游连接。
	outboundConn, err := net.DialTCP("tcp", nil, upstreamAddr)
	if err != nil {
		com.Log.Errorf("dial upstream %s failed: %v",
			p.config.Upstream.Url, err)
		_ = wrappedInboundConn.Close()
		return
	}

	func(inbound, outbound net.Conn) {
		err := p.handleTCPClient(inbound, outbound)
		if err != nil {
			com.Log.Errorf("handle client failed: %v", err)
		}
	}(wrappedInboundConn, outboundConn)
}

func (p *Proxy) Run() {
	server, err := net.Listen("tcp", p.config.Listen)
	if err != nil {
		com.Log.Errorf("listen %s failed: %v", p.config.Listen, err)
		return
	}
	defer server.Close()

	com.Log.Infof("start to listen %s", p.config.Listen)

	for {
		inboundConn, err := server.Accept()
		if err != nil {
			com.Log.Errorf("accept failed: %v", err)
			continue
		}

		com.Log.Debugf("client %s connected", inboundConn.RemoteAddr())
		go p.handleConnection(inboundConn, p.config.Upstream.Url)
	}
}


func closeWrite(conn net.Conn) {
	if conn, ok := conn.(interface {
		CloseWrite() error
	}); ok {
		_ = conn.CloseWrite()
		return
	}

	_ = conn.Close()
}

func (proxy *Proxy) handleTCPClient(inboundConn net.Conn, outboundConn net.Conn) error {
	var wait sync.WaitGroup
	wait.Add(2)

	errCh := make(chan error, 2)

	go func() {
		defer wait.Done()

		// 下游 -> 上游
		err := proxy.handleUpstream(
			inboundConn,
			outboundConn,
		)

		errCh <- err

		if err != nil {
			// 异常时关闭两端，唤醒另一个方向。
			_ = inboundConn.Close()
			_ = outboundConn.Close()
			return;
		}
		// 正常 EOF，只关闭上游写方向。
		closeWrite(outboundConn)
	}()

	go func() {
		defer wait.Done()

		// 上游 -> 下游
		err := proxy.handleDownstream(
			outboundConn,
			inboundConn,
		)

		errCh <- err

		if err != nil {
			_ = inboundConn.Close()
			_ = outboundConn.Close()
			return;
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

	_ = inboundConn.Close()
	_ = outboundConn.Close()
	return firstErr
}

func (proxy *Proxy) handleUpstream(srcConn net.Conn, dstConn net.Conn) error {
	// 下游接收数据时使用下游 Codec 解码。
	decoder, err := NewCodec(
		proxy.config.CryptoEnabled,
		proxy.config.Key,
	)
	if err != nil {
		return err
	}

	// 向上游发送数据时使用上游 Codec 编码。
	encoder, err := NewCodec(proxy.config.Upstream.CryptoEnabled, proxy.config.Upstream.Key)
	if err != nil {
		return err
	}

	_, err = copyBuffer(dstConn, srcConn, nil, decoder, encoder)
	return err
}

func (proxy *Proxy) handleDownstream(srcConn net.Conn, dstConn net.Conn) error {
	// 上游接收数据时使用上游 Codec 解码。
	decoder, err := NewCodec(
		proxy.config.Upstream.CryptoEnabled,
		proxy.config.Upstream.Key,
	)
	if err != nil {
		return err
	}

	// 向下游发送数据时使用下游 Codec 编码。
	encoder, err := NewCodec(proxy.config.CryptoEnabled, proxy.config.Key)
	if err != nil {
		return err
	}

	_, err = copyBuffer(dstConn, srcConn, nil, decoder, encoder)
	return err
}