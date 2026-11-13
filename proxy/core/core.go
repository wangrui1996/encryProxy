package core

import (
	"context"
	"fmt"
	"net"
	"sync"

	"wangjui.com/enproxy/proxy/common"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/stat"
)

var Logger = common.Log

// Account 是认证成功后的运行时账号。
// RateLimit 在账号创建时初始化，同一账号的所有连接共享限速器。
type Account struct {
	Name      string
	RateLimit *RateLimit
}

// Authenticator 由外部 AccountManager 实现。
// core 不需要知道账号是如何存储和管理的。
type Authenticator interface {
	Authenticate(username, password string) (*Account, error)
}

// RouteResolver 根据入站 Tag 选择上游。
type RouteResolver interface {
	Resolve(inboundTag string) (*config.OutboundConfig, error)
}

// Dialer 负责建立上游连接。
type Dialer interface {
	DialContext(ctx context.Context, address string) (net.Conn, error)
}

// Proxy 表示一个独立的入站代理实例。
type Proxy struct {
	inbound config.InboundConfig

	authenticator Authenticator
	routeResolver RouteResolver
	dialer        Dialer

	portRateLimit *RateLimit
	traffic       *stat.Traffic
	trafficMgr    *stat.Manager
}

// Dependencies 是创建 Proxy 所需的外部依赖。
type Dependencies struct {
	Authenticator Authenticator
	RouteResolver RouteResolver
	Dialer        Dialer
	Traffic       *stat.Manager
}

// NewProxy 创建单个入站代理。
func NewProxy(
	inbound config.InboundConfig,
	deps Dependencies,
) (*Proxy, error) {
	if deps.RouteResolver == nil {
		return nil, fmt.Errorf("route resolver is nil")
	}
	if deps.Dialer == nil {
		return nil, fmt.Errorf("outbound dialer is nil")
	}
	if deps.Traffic == nil {
		return nil, fmt.Errorf("traffic manager is nil")
	}
	if inbound.AuthRequired && deps.Authenticator == nil {
		return nil, fmt.Errorf(
			"inbound %q requires authentication, but authenticator is nil",
			inbound.Tag,
		)
	}

	return &Proxy{
		inbound:       inbound,
		authenticator: deps.Authenticator,
		routeResolver: deps.RouteResolver,
		dialer:        deps.Dialer,
		portRateLimit: NewRateLimit(inbound.RateLimit),
		traffic:       deps.Traffic.Register(inbound.Listen),
		trafficMgr:    deps.Traffic,
	}, nil
}

// Run 启动 TCP 监听。
func (p *Proxy) Run() {
	server, err := net.Listen("tcp", p.inbound.Listen)
	if err != nil {
		Logger.Errorf("listen %s failed: %v", p.inbound.Listen, err)
		return
	}
	defer server.Close()

	Logger.Infof(
		"inbound %s listening on %s",
		p.inbound.Tag,
		p.inbound.Listen,
	)

	for {
		inboundConn, err := server.Accept()
		if err != nil {
			Logger.Errorf(
				"accept on %s failed: %v",
				p.inbound.Listen,
				err,
			)
			continue
		}

		Logger.Debugf(
			"client %s connected to %s",
			inboundConn.RemoteAddr(),
			p.inbound.Tag,
		)

		go p.handleConnection(inboundConn)
	}
}

func (p *Proxy) handleConnection(inboundConn net.Conn) {
	defer inboundConn.Close()

	var account *Account
	accountName := "anonymous"

	if p.inbound.AuthRequired {
		var err error

		account, err = p.authenticate(inboundConn)
		if err != nil {
			Logger.Errorf(
				"client %s authentication failed on %s: %v",
				inboundConn.RemoteAddr(),
				p.inbound.Tag,
				err,
			)
			return
		}

		accountName = account.Name

		Logger.Debugf(
			"client %s authenticated as %s",
			inboundConn.RemoteAddr(),
			account.Name,
		)
	}

	outbound, err := p.routeResolver.Resolve(p.inbound.Tag)
	if err != nil {
		Logger.Errorf(
			"resolve route for inbound %s failed: %v",
			p.inbound.Tag,
			err,
		)
		return
	}

	outboundConn, err := p.dialer.DialContext(
		context.Background(),
		outbound.Server,
	)
	if err != nil {
		Logger.Errorf(
			"dial outbound %s (%s) failed: %v",
			outbound.Tag,
			outbound.Server,
			err,
		)
		return
	}
	defer outboundConn.Close()

	if outbound.Account != nil {
		if err := authenticateOutbound(
			outboundConn,
			outbound.Account,
			outbound.Crypto,
		); err != nil {
			Logger.Errorf(
				"authenticate outbound %s (%s) failed: %v",
				outbound.Tag,
				outbound.Server,
				err,
			)
			return
		}

		Logger.Debugf(
			"authenticated to outbound %s as %s",
			outbound.Tag,
			outbound.Account.Username,
		)
	}

	var clientConn net.Conn = inboundConn

	// 保持原有包装顺序：端口限速 -> 用户限速 -> 流量统计。
	if p.portRateLimit != nil {
		clientConn = NewRateLimitedConn(
			clientConn,
			p.portRateLimit,
		)
	}

	if account != nil && account.RateLimit != nil {
		clientConn = NewRateLimitedConn(
			clientConn,
			account.RateLimit,
		)
	}

	userTraffic := p.trafficMgr.RegisterUser(accountName)
	clientConn = stat.NewTrafficConn(
		clientConn,
		p.traffic,
		userTraffic,
	)

	if err := p.handleTCPClient(
		clientConn,
		outboundConn,
		outbound,
	); err != nil {
		Logger.Errorf(
			"handle client %s on %s failed: %v",
			inboundConn.RemoteAddr(),
			p.inbound.Tag,
			err,
		)
	}
}

func closeWrite(conn net.Conn) {
	if conn, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = conn.CloseWrite()
		return
	}

	_ = conn.Close()
}

func (p *Proxy) handleTCPClient(
	inboundConn net.Conn,
	outboundConn net.Conn,
	outbound *config.OutboundConfig,
) error {
	if outbound == nil {
		return fmt.Errorf("outbound is nil")
	}

	var wait sync.WaitGroup
	wait.Add(2)

	errCh := make(chan error, 2)

	go func() {
		defer wait.Done()

		err := p.handleUpstream(
			inboundConn,
			outboundConn,
			outbound,
		)
		if err != nil {
			err = fmt.Errorf("downstream -> upstream: %w", err)
		}

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

		err := p.handleDownstream(
			outboundConn,
			inboundConn,
			outbound,
		)
		if err != nil {
			err = fmt.Errorf("upstream -> downstream: %w", err)
		}

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