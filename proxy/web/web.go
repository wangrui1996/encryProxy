package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"wangjui.com/enproxy/proxy/common"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/stat"
)

var Logger = common.Log

type WebServer struct {
	addr     string
	traffic  *stat.Manager
	inbounds []config.InboundConfig
	accounts []config.AccountConfig
	server   *http.Server
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

func NewWebServer(
	addr string,
	traffic *stat.Manager,
	inbounds []config.InboundConfig,
	accounts []config.AccountConfig,
) *WebServer {
	return &WebServer{
		addr:     addr,
		traffic:  traffic,
		inbounds: append([]config.InboundConfig(nil), inbounds...),
		accounts: append([]config.AccountConfig(nil), accounts...),
	}
}

func (w *WebServer) Run() {
	if err := w.Start(); err != nil && err != http.ErrServerClosed {
		Logger.Errorf("web server failed: %v", err)
	}
}

func (w *WebServer) Start() error {
	if w == nil || w.traffic == nil {
		return fmt.Errorf("web server requires a traffic manager")
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

	Logger.Infof("Web dashboard listening on http://%s", w.addr)

	return w.server.ListenAndServe()
}

func (w *WebServer) Shutdown(ctx context.Context) error {
	if w == nil || w.server == nil {
		return nil
	}

	return w.server.Shutdown(ctx)
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

func (w *WebServer) handleIndex(
	rw http.ResponseWriter,
	r *http.Request,
) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}

	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")

	_, _ = rw.Write([]byte(webUI))
}

func (w *WebServer) handleSummary(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetSummary())
}

func (w *WebServer) handleRealtime(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetRealtime())
}

func (w *WebServer) handleHourly(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetHourlyHistory())
}

func (w *WebServer) handleDaily(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetDailyHistory())
}

func (w *WebServer) handleMonthly(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetMonthlyHistory())
}

func (w *WebServer) handleLimits(
	rw http.ResponseWriter,
	r *http.Request,
) {
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