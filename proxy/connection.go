package proxy

import (
	"context"
	"fmt"
	"net"

	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/stat"
)

// handleConnection owns the composition of authentication, rate limit,
// wire-byte traffic accounting and application protocol handling.
func (p *Proxy) handleConnection(inboundConn net.Conn) {
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
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
	trafficConn := stat.NewTrafficConn(accountLimited, p.traffic, p.trafficMgr.RegisterUser(accountName))

	clientConn, err := wrapCrypto(trafficConn, p.inbound.Crypto)
	if err != nil {
		Logger.Errorf("create inbound encryption stream: %v", err)
		return
	}

	proxyAuthenticate := func(username, password string) error {
		if p.accountManager == nil {
			return fmt.Errorf("proxy account manager is nil")
		}
		acc, err := p.accountManager.AuthenticateProxy(username, password)
		if err != nil {
			return err
		}
		if acc == nil {
			return fmt.Errorf("proxy account manager returned nil account")
		}
		// An authenticated custom crypto account takes precedence over the
		// proxy protocol account for limit and traffic attribution.
		if cryptoAccount == nil {
			accountLimited.SetLimit(acc.RateLimit)
			trafficConn.SetUserTraffic(p.trafficMgr.RegisterUser(acc.Name))
		}
		return nil
	}

	if err := p.handler.Handle(ctx, clientConn, proxyAuthenticate); err != nil {
		Logger.Errorf("client %s on %s: %v", inboundConn.RemoteAddr(), p.inbound.Tag, err)
	}
}
