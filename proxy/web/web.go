package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"wangjui.com/enproxy/proxy/common"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/stat"
)

var Logger = common.Log

type WebServer struct {
	addr         string
	traffic      *stat.Manager
	inbounds     []config.InboundConfig
	accounts     []config.AccountConfig
	server       *http.Server
	mu           sync.Mutex
	started      bool
	closed       bool
	done         chan struct{}
	runErr       error
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
}

// InboundLimitInfo 保留旧字段，兼容现有 WebUI。
type InboundLimitInfo struct {
	Tag                string                 `json:"tag"`
	Listen             string                 `json:"listen"`
	Protocol           string                 `json:"protocol"`
	AuthRequired       bool                   `json:"auth_required"`
	CryptoEnabled      bool                   `json:"crypto_enabled"`
	CryptoAuthRequired bool                   `json:"crypto_auth_required"`
	ProxyAuthRequired  bool                   `json:"proxy_auth_required"`
	RateLimit          config.RateLimitConfig `json:"rate_limit"`
}

// AccountLimitInfo 保留 enabled 兼容现有 WebUI。
type AccountLimitInfo struct {
	Name         string                 `json:"name"`
	Enabled      bool                   `json:"enabled"`
	CryptoEnable bool                   `json:"crypto_enable"`
	ProxyEnable  bool                   `json:"proxy_enable"`
	RateLimit    config.RateLimitConfig `json:"rate_limit"`
}

func NewWebServer(addr string, traffic *stat.Manager, inbounds []config.InboundConfig, accounts []config.AccountConfig) *WebServer {
	w := &WebServer{
		done:         make(chan struct{}),
		shutdownDone: make(chan struct{}),
		addr:         addr,
		traffic:      traffic,
		inbounds:     append([]config.InboundConfig(nil), inbounds...),
		accounts:     append([]config.AccountConfig(nil), accounts...),
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/", w.handleIndex)
	mux.HandleFunc("/api/traffic/summary", w.handleSummary)
	mux.HandleFunc("/api/traffic/realtime", w.handleRealtime)
	mux.HandleFunc("/api/traffic/history/hourly", w.handleHourly)
	mux.HandleFunc("/api/traffic/history/daily", w.handleDaily)
	mux.HandleFunc("/api/traffic/history/monthly", w.handleMonthly)
	mux.HandleFunc("/api/limits", w.handleLimits)

	w.server = &http.Server{
		Addr:              w.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return w
}

// Start binds synchronously and returns once the listener is ready.
func (w *WebServer) Start() error {
	if w == nil || w.traffic == nil {
		return fmt.Errorf("web server requires a traffic manager")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("web server is closed")
	}
	if w.started {
		return nil
	}
	listener, err := net.Listen("tcp", w.addr)
	if err != nil {
		return fmt.Errorf("listen web %s: %w", w.addr, err)
	}
	w.started = true
	go func() {
		err := w.server.Serve(listener)
		w.mu.Lock()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			w.runErr = err
		}
		w.mu.Unlock()
		close(w.done)
	}()
	return nil
}

// Run retains the original blocking entry point.
func (w *WebServer) Run() {
	if err := w.Start(); err != nil {
		Logger.Errorf("web start: %v", err)
		return
	}
	if err := w.Wait(); err != nil {
		Logger.Errorf("web serve: %v", err)
	}
}

func (w *WebServer) Wait() error {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started {
		return fmt.Errorf("web server has not started")
	}
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runErr
}

// Shutdown prevents future starts, drains requests, and force-closes on timeout.
// Concurrent callers can independently stop waiting when their context expires.
func (w *WebServer) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.shutdownOnce.Do(func() {
		go func() {
			err := w.server.Shutdown(ctx)
			if err != nil {
				_ = w.server.Close()
			}
			w.mu.Lock()
			started := w.started
			w.mu.Unlock()
			if started {
				<-w.done
			}
			w.shutdownErr = err
			close(w.shutdownDone)
		}()
	})
	select {
	case <-w.shutdownDone:
		return w.shutdownErr
	case <-ctx.Done():
		_ = w.server.Close()
		return ctx.Err()
	}
}

func writeJSON(rw http.ResponseWriter, data interface{}) {
	rw.Header().Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	if err := json.NewEncoder(rw).Encode(data); err != nil {
		Logger.Errorf("encode JSON failed: %v", err)
	}
}

func (w *WebServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}

	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")

	_, _ = rw.Write([]byte(webUI))
}

func (w *WebServer) handleSummary(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, w.traffic.GetSummary())
}

func (w *WebServer) handleRealtime(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, w.traffic.GetRealtime())
}

func (w *WebServer) handleHourly(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, w.traffic.GetHourlyHistory())
}

func (w *WebServer) handleDaily(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, w.traffic.GetDailyHistory())
}

func (w *WebServer) handleMonthly(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, w.traffic.GetMonthlyHistory())
}

func (w *WebServer) handleLimits(rw http.ResponseWriter, r *http.Request) {
	inbounds := make([]InboundLimitInfo, 0, len(w.inbounds))

	for _, in := range w.inbounds {
		cryptoEnabled := in.Crypto != nil

		cryptoAuthRequired := cryptoEnabled &&
			in.Crypto.AuthRequired

		proxyAuthRequired := in.Protocol == "http" &&
			in.HTTP != nil &&
			in.HTTP.AuthRequired

		inbounds = append(inbounds, InboundLimitInfo{
			Tag:                in.Tag,
			Listen:             in.Listen,
			Protocol:           in.Protocol,
			AuthRequired:       cryptoAuthRequired || proxyAuthRequired,
			CryptoEnabled:      cryptoEnabled,
			CryptoAuthRequired: cryptoAuthRequired,
			ProxyAuthRequired:  proxyAuthRequired,
			RateLimit:          in.RateLimit,
		})
	}

	accounts := make([]AccountLimitInfo, 0, len(w.accounts))

	for _, account := range w.accounts {
		accounts = append(accounts, AccountLimitInfo{
			Name:         account.Name,
			Enabled:      account.CryptoEnable || account.ProxyEnable,
			CryptoEnable: account.CryptoEnable,
			ProxyEnable:  account.ProxyEnable,
			RateLimit:    account.RateLimit,
		})
	}

	writeJSON(rw, map[string]interface{}{
		"inbounds": inbounds,
		"accounts": accounts,
	})
}
