package proxy

import (
    "fmt"
    "net"
    "sync"

    "wangjui.com/enproxy/proxy/common"
)
var Logger = common.Log

type Proxy struct {
    inbound        InboundConfig
    accounts       *AccountManager
    routeManager   *RouteManager
    portRateLimit  *RateLimit
    traffic        *Traffic
    trafficManager *TrafficManager
}

func NewProxy(inbound InboundConfig, accounts *AccountManager, routeManager *RouteManager, trafficManager *TrafficManager) *Proxy {
    return &Proxy{
        inbound: inbound, accounts: accounts, routeManager: routeManager,
        portRateLimit:  NewRateLimit(inbound.RateLimit),
        traffic:        trafficManager.Register(inbound.Listen),
        trafficManager: trafficManager,
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
            Log.Errorf("client %s authentication failed on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
            return
        }
        accountName = account.Name
        Log.Debugf("client %s authenticated as %s", inboundConn.RemoteAddr(), account.Name)
    }
    outbound, err := p.routeManager.Resolve(p.inbound.Tag)
    if err != nil {
        Log.Errorf("resolve route for inbound %s failed: %v", p.inbound.Tag, err)
        return
    }
    outboundConn, err := net.Dial("tcp", outbound.Server)
    if err != nil {
        Log.Errorf("dial outbound %s (%s) failed: %v", outbound.Tag, outbound.Server, err)
        return
    }
    defer outboundConn.Close()
    if outbound.Account != nil {
        if err := authenticateOutbound(outboundConn, outbound.Account, outbound.Crypto); err != nil {
            Log.Errorf("authenticate outbound %s (%s) failed: %v", outbound.Tag, outbound.Server, err)
            return
        }
        Log.Debugf("authenticated to outbound %s as %s", outbound.Tag, outbound.Account.Username)
    }
    var clientConn net.Conn = inboundConn
    if p.portRateLimit != nil {
        clientConn = NewRateLimitedConn(clientConn, p.portRateLimit)
    }
    if account != nil && account.RateLimit != nil {
        clientConn = NewRateLimitedConn(clientConn, account.RateLimit)
    }
    userTraffic := p.trafficManager.RegisterUser(accountName)
    clientConn = NewTrafficConn(clientConn, p.traffic, userTraffic)
    if err := p.handleTCPClient(clientConn, outboundConn, outbound); err != nil {
        Log.Errorf("handle client %s on %s failed: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
    }
}

func (p *Proxy) Run() {
    server, err := net.Listen("tcp", p.inbound.Listen)
    if err != nil {
        Log.Errorf("listen %s failed: %v", p.inbound.Listen, err)
        return
    }
    defer server.Close()
    Log.Infof("inbound %s listening on %s", p.inbound.Tag, p.inbound.Listen)
    for {
        inboundConn, err := server.Accept()
        if err != nil {
            Log.Errorf("accept on %s failed: %v", p.inbound.Listen, err)
            continue
        }
        Log.Debugf("client %s connected to %s", inboundConn.RemoteAddr(), p.inbound.Tag)
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
        err := p.handleDownstream(outboundConn, inboundConn, outbound)
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
