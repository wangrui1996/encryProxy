package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/protocol"
	"wangjui.com/enproxy/proxy/stat"
)

// Account 是认证成功后的运行时账号，账号级限速器为所有连接共享。
type Account struct {
	Name      string
	RateLimit *core.RateLimit
}

// Proxy 管理一个入站监听端口及其连接的公共处理逻辑。
type Proxy struct {
	inbound       config.InboundConfig
	authenticator Authenticator
	handler       protocol.Handler

	portRateLimit *core.RateLimit
	traffic       *stat.Traffic
	trafficMgr    *stat.Manager

	listenerMu    sync.Mutex
	listener      net.Listener
	stopCh        chan struct{}
	stopOnce      sync.Once
	ctx           context.Context
	cancel        context.CancelFunc
	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
}

func NewProxy(inbound config.InboundConfig, deps Dependencies) (*Proxy, error) {
	if err := inbound.Validate(); err != nil {
		return nil, fmt.Errorf("inbound %q: %w", inbound.Tag, err)
	}
	if deps.Traffic == nil {
		return nil, fmt.Errorf("traffic manager is nil")
	}
	if inbound.Crypto != nil && inbound.Crypto.AuthRequired && deps.Authenticator == nil {
		return nil, fmt.Errorf("inbound %q needs crypto authenticator", inbound.Tag)
	}
	if inbound.Protocol == "http" && inbound.HTTP.AuthRequired && deps.Authenticator == nil {
		return nil, fmt.Errorf("inbound %q needs HTTP proxy authenticator", inbound.Tag)
	}

	if deps.RouteResolver == nil || deps.Dialer == nil {
		return nil, fmt.Errorf("inbound %q: route resolver and dialer are required", inbound.Tag)
	}
	protocolDial := newProtocolDial(deps.Dialer)

	var handler protocol.Handler
	switch inbound.Protocol {
	case "tcp":
		var err error
		handler, err = protocol.NewTCP(inbound, deps.RouteResolver, protocolDial, core.Relay)
		if err != nil {
			return nil, err
		}
	case "http":
		var err error
		handler, err = protocol.NewHTTP(inbound, deps.RouteResolver, protocolDial, core.Relay)
		if err != nil {
			return nil, err
		}
	case "shadowsocks":
		var err error
		handler, err = protocol.NewShadowsocks(inbound, deps.RouteResolver, protocolDial, core.Relay)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("inbound %q: unsupported protocol %q", inbound.Tag, inbound.Protocol)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Proxy{
		inbound:       inbound,
		authenticator: deps.Authenticator,
		handler:       handler,
		portRateLimit: core.NewRateLimit(inbound.RateLimit),
		traffic:       deps.Traffic.Register(inbound.Listen),
		trafficMgr:    deps.Traffic,
		stopCh:        make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
		connections:   make(map[net.Conn]struct{}),
	}, nil
}

// Run 阻塞运行入站监听器，直到 Close 被调用或监听失败。
func (p *Proxy) Run() {
	listener, err := net.Listen("tcp", p.inbound.Listen)
	if err != nil {
		Logger.Errorf("listen %s (%s) failed: %v", p.inbound.Tag, p.inbound.Listen, err)
		return
	}
	p.listenerMu.Lock()
	select {
	case <-p.stopCh:
		p.listenerMu.Unlock()
		_ = listener.Close()
		return
	default:
		p.listener = listener
	}
	p.listenerMu.Unlock()
	defer func() {
		_ = listener.Close()
		p.listenerMu.Lock()
		if p.listener == listener {
			p.listener = nil
		}
		p.listenerMu.Unlock()
	}()

	Logger.Infof("inbound %s listening on %s", p.inbound.Tag, listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				Logger.Warnf("temporary accept failure on %s: %v", p.inbound.Tag, err)
				continue
			}
			Logger.Errorf("accept on %s failed: %v", p.inbound.Tag, err)
			return
		}
		p.connectionsMu.Lock()
		select {
		case <-p.stopCh:
			p.connectionsMu.Unlock()
			_ = conn.Close()
			return
		default:
			p.connections[conn] = struct{}{}
		}
		p.connectionsMu.Unlock()
		go func(c net.Conn) {
			defer func() {
				_ = c.Close()
				p.connectionsMu.Lock()
				delete(p.connections, c)
				p.connectionsMu.Unlock()
			}()
			p.handleConnection(c)
		}(conn)
	}
}

// Close 停止监听，同时关闭当前活动连接；可以重复调用。
func (p *Proxy) Close() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { p.cancel(); close(p.stopCh) })
	p.listenerMu.Lock()
	if p.listener != nil {
		_ = p.listener.Close()
	}
	p.listenerMu.Unlock()
	p.connectionsMu.Lock()
	for c := range p.connections {
		_ = c.Close()
	}
	p.connectionsMu.Unlock()
}
