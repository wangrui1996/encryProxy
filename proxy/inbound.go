package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
	"strconv"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/protocol"
	"wangjui.com/enproxy/proxy/route"
	"wangjui.com/enproxy/proxy/stat"
)

// Proxy 管理一个入站监听端口及其连接的公共处理逻辑。
type Proxy struct {
	inbound        config.InboundConfig
	accountManager *AccountManager
	handler        protocol.Handler

	portRateLimit *core.RateLimit
	traffic       *stat.Traffic
	trafficMgr    *stat.Manager

	lifecycleMu   sync.Mutex
	started       bool
	closed        bool
	closeOnce     sync.Once
	workers       sync.WaitGroup
	serveDone     chan struct{}
	runErr        error
	listener      net.Listener
	stopCh        chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
}

// NewProxy 使用 ProxyManager 创建的具体组件组装一个入站监听器。
// 根包不再维护 Dependencies 聚合结构或额外的依赖接口。
func NewProxy(inbound config.InboundConfig, accounts *AccountManager, routes *route.Manager, outbounds *OutboundManager, traffic *stat.Manager) (*Proxy, error) {
	if err := inbound.Validate(); err != nil {
		return nil, fmt.Errorf("inbound %q: %w", inbound.Tag, err)
	}
	if traffic == nil {
		return nil, fmt.Errorf("traffic manager is nil")
	}
	if inbound.Crypto != nil && inbound.Crypto.AuthRequired && accounts == nil {
		return nil, fmt.Errorf("inbound %q needs crypto authenticator", inbound.Tag)
	}
	if inbound.Protocol == "http" && inbound.HTTP.AuthRequired && accounts == nil {
		return nil, fmt.Errorf("inbound %q needs HTTP proxy authenticator", inbound.Tag)
	}

	if routes == nil || outbounds == nil {
		return nil, fmt.Errorf("inbound %q: route manager and outbound manager are required", inbound.Tag)
	}
	ctx, cancel := context.WithCancel(context.Background())
	egress := inboundOutbounds{OutboundManager: outbounds, parent: ctx}
	handler, err := protocol.NewHandler(protocolInboundOptions(inbound), routes, egress, core.RelayContext)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Proxy{
		inbound:        inbound,
		accountManager: accounts,
		handler:        handler,
		portRateLimit:  core.NewRateLimit(inbound.RateLimit.UploadKbps, inbound.RateLimit.DownloadKbps),
		traffic:        traffic.Register(inbound.Listen),
		trafficMgr:     traffic,
		stopCh:         make(chan struct{}),
		serveDone:      make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		connections:    make(map[net.Conn]struct{}),
	}, nil
}

// Start synchronously binds the port, then serves in the background.
// A closed Proxy cannot be restarted.
func (p *Proxy) Start() error {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.closed {
		return fmt.Errorf("inbound %s is closed", p.inbound.Tag)
	}
	if p.started {
		return nil
	}
	listener, err := net.Listen("tcp", p.inbound.Listen)
	if err != nil {
		return fmt.Errorf("listen %s (%s): %w", p.inbound.Tag, p.inbound.Listen, err)
	}
	p.listener = listener
	p.started = true
	go p.serve(listener)
	return nil
}

func (p *Proxy) serve(listener net.Listener) {
	defer close(p.serveDone)
	defer listener.Close()
	var delay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
				}
				if delay > time.Second {
					delay = time.Second
				}
				timer := time.NewTimer(delay)
				select {
				case <-p.stopCh:
					timer.Stop()
					return
				case <-timer.C:
				}
				continue
			}
			p.lifecycleMu.Lock()
			p.runErr = fmt.Errorf("accept %s: %w", p.inbound.Tag, err)
			p.lifecycleMu.Unlock()
			go p.Close()
			return
		}
		delay = 0
		p.connectionsMu.Lock()
		select {
		case <-p.stopCh:
			p.connectionsMu.Unlock()
			conn.Close()
			return
		default:
			p.connections[conn] = struct{}{}
			p.workers.Add(1)
		}
		p.connectionsMu.Unlock()
		go func(c net.Conn) {
			defer p.workers.Done()
			defer func() { c.Close(); p.connectionsMu.Lock(); delete(p.connections, c); p.connectionsMu.Unlock() }()
			p.handleConnection(c)
		}(conn)
	}
}

// Wait reports a terminal accept error; shutdown errors are not failures.
func (p *Proxy) Wait() error {
	p.lifecycleMu.Lock()
	started := p.started
	p.lifecycleMu.Unlock()
	if !started {
		return fmt.Errorf("inbound has not started")
	}
	<-p.serveDone
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.runErr
}

// Close stops acceptance, cancels sessions and waits for all handlers.
func (p *Proxy) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		p.lifecycleMu.Lock()
		p.closed = true
		p.cancel()
		close(p.stopCh)
		started, listener := p.started, p.listener
		p.lifecycleMu.Unlock()
		if listener != nil {
			listener.Close()
		}
		// No new workers can be added after serve has returned.
		if started {
			<-p.serveDone
		}
		p.connectionsMu.Lock()
		conns := make([]net.Conn, 0, len(p.connections))
		for c := range p.connections {
			conns = append(conns, c)
		}
		p.connectionsMu.Unlock()
		for _, c := range conns {
			c.Close()
		}
		p.workers.Wait()
		if closer, ok := p.handler.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				Logger.Errorf("close handler: %v", err)
			}
		}
	})
}

// handleConnection owns the composition of authentication, rate limit,
// wire-byte traffic accounting and application protocol handling.
func (p *Proxy) handleConnection(inboundConn net.Conn) {
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	// 必须在原始 socket 上获取 NAT 目标；限速/统计/加密连接包装后无法调用 getsockopt。
	// 未经 REDIRECT 的普通 HTTP 正向代理连接保留原来的处理流程。
	if p.inbound.Protocol == "http" && p.inbound.HTTP != nil && p.inbound.HTTP.Sniff {
		original, err := originalDestination(inboundConn)
		if err == nil && isRedirectedDestination(inboundConn, original) {
			ctx = protocol.WithOriginalDestination(ctx, original)
		}
	}
	accountName := "anonymous"
	var cryptoAccount *Account
	if p.inbound.Crypto != nil && p.inbound.Crypto.AuthRequired {
		var err error
		cryptoAccount, err = p.authenticate(inboundConn)
		if err != nil {
			Logger.Warnf("client %s authentication failed on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
			return
		}
		accountName = cryptoAccount.Name
	}

	// Wire traffic is rate-limited and counted before decryption (same as before).
	// Port and account limits remain independent; only ONE account limiter exists.
	portLimited := core.NewRateLimitedConnWithContext(p.ctx, inboundConn, p.portRateLimit)
	defer portLimited.Close()
	var initialLimit *core.RateLimit
	if cryptoAccount != nil {
		initialLimit = cryptoAccount.RateLimit
	}
	accountLimited := core.NewRateLimitedConnWithContext(p.ctx, portLimited, initialLimit)
	defer accountLimited.Close()
	defaultTraffic := p.trafficMgr.RegisterUser(accountName)
	trafficConn := stat.NewTrafficConn(accountLimited, p.traffic, defaultTraffic)

	clientConn, err := wrapCrypto(trafficConn, p.inbound.Crypto)
	if err != nil {
		Logger.Errorf("create inbound encryption stream: %v", err)
		return
	}

	proxyAuthenticate := func(username, password string) (func(), error) {
		if p.accountManager == nil {
			return nil, fmt.Errorf("proxy account manager is nil")
		}
		acc, err := p.accountManager.AuthenticateProxy(username, password)
		if err != nil {
			return nil, err
		}
		if acc == nil {
			return nil, fmt.Errorf("proxy account manager returned nil account")
		}
		// An authenticated custom crypto account takes precedence over the
		// proxy protocol account for limit and traffic attribution.
		if cryptoAccount != nil {
			return nil, nil
		}

		accountLimited.SetLimit(acc.RateLimit)
		trafficConn.SetUserTraffic(p.trafficMgr.RegisterUser(acc.Name))
		return func() {
			// Restore defaults before the next keep-alive request is read.
			accountLimited.SetLimit(initialLimit)
			trafficConn.SetUserTraffic(defaultTraffic)
		}, nil
	}

	if err := p.handler.Handle(ctx, clientConn, proxyAuthenticate); err != nil {
		Logger.Errorf("client %s on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
	}
}

func protocolHTTPOptions(cfg *config.HTTPConfig) *protocol.HTTPOptions {
	if cfg == nil {
		return nil
	}
	return &protocol.HTTPOptions{AuthRequired: cfg.AuthRequired, Account: cfg.Account, Passwd: cfg.Passwd, Sniff: cfg.Sniff}
}

func protocolShadowsocksOptions(cfg *config.ShadowsocksConfig) *protocol.ShadowsocksOptions {
	if cfg == nil {
		return nil
	}
	return &protocol.ShadowsocksOptions{Method: cfg.Method, Password: cfg.Password}
}

func protocolInboundOptions(in config.InboundConfig) protocol.InboundOptions {
	return protocol.InboundOptions{Tag: in.Tag, Protocol: in.Protocol, HTTP: protocolHTTPOptions(in.HTTP), Shadowsocks: protocolShadowsocksOptions(in.Shadowsocks)}
}

// isRedirectedDestination prevents interpreting a regular explicit HTTP proxy
// connection as a transparent connection when SO_ORIGINAL_DST happens to return
// the socket's own destination (e.g. Linux without NAT interception).
func isRedirectedDestination(conn net.Conn, original string) bool {
	host, port, err := net.SplitHostPort(original)
	if err != nil {
		return false
	}
	origIP := net.ParseIP(host)
	origPort, err := strconv.Atoi(port)
	if err != nil || origIP == nil || origPort < 1 || origPort > 65535 {
		return false
	}
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok || local == nil || local.IP == nil {
		return false
	}
	return origPort != local.Port || !origIP.Equal(local.IP)
}