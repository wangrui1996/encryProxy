package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"wangjui.com/spider/proxy/common"
	"wangjui.com/spider/proxy/config"
	"wangjui.com/spider/proxy/stat"
)

var Logger = common.Log

type WebServer struct {
	authConfig    config.WebConfig
	authMu        sync.Mutex
	sessions      map[string]time.Time
	loginWindow   time.Time
	loginAttempts int
	addr          string
	traffic       *stat.Manager
	inbounds      []config.InboundConfig
	accounts      []config.AccountConfig
	server        *http.Server
	mu            sync.Mutex
	started       bool
	closed        bool
	done          chan struct{}
	runErr        error
	shutdownOnce  sync.Once
	shutdownDone  chan struct{}
	shutdownErr   error
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

func NewWebServer(cfg config.WebConfig, traffic *stat.Manager, inbounds []config.InboundConfig, accounts []config.AccountConfig) *WebServer {
	w := &WebServer{
		done:         make(chan struct{}),
		shutdownDone: make(chan struct{}),
		addr:         cfg.Listen,
		authConfig:   cfg,
		sessions:     make(map[string]time.Time),
		traffic:      traffic,
		inbounds:     append([]config.InboundConfig(nil), inbounds...),
		accounts:     append([]config.AccountConfig(nil), accounts...),
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/login", w.handleLogin)
	mux.HandleFunc("/logout", w.handleLogout)
	mux.HandleFunc("/", w.handleIndex)
	mux.HandleFunc("/api/traffic/summary", w.handleSummary)
	mux.HandleFunc("/api/traffic/realtime", w.handleRealtime)
	mux.HandleFunc("/api/traffic/history/hourly", w.handleHourly)
	mux.HandleFunc("/api/traffic/history/daily", w.handleDaily)
	mux.HandleFunc("/api/traffic/history/monthly", w.handleMonthly)
	mux.HandleFunc("/api/limits", w.handleLimits)

	w.server = &http.Server{
		Addr:              w.addr,
		Handler:           w.requireLogin(mux),
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return w
}

// Start binds synchronously and returns once the listener is ready.
func (w *WebServer) Start() error {
	if w == nil || w.traffic == nil {
		return fmt.Errorf("web server requires a traffic manager")
	}
	if strings.TrimSpace(w.authConfig.User) == "" || w.authConfig.Passwd == "" {
		return fmt.Errorf("web user and passwd are required")
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
	rw.Header().Set("Cache-Control", "no-store")

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

const sessionCookie = "spider_session"
const sessionTTL = 12 * time.Hour

func (w *WebServer) authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	w.authMu.Lock()
	defer w.authMu.Unlock()
	expiry, ok := w.sessions[c.Value]
	if ok && time.Now().Before(expiry) {
		return true
	}
	delete(w.sessions, c.Value)
	return false
}

func (w *WebServer) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		rw.Header().Set("X-Frame-Options", "DENY")
		rw.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/login" {
			w.handleLogin(rw, r)
			return
		}
		if !w.authenticated(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				rw.Header().Set("Content-Type", "application/json; charset=utf-8")
				rw.WriteHeader(http.StatusUnauthorized)
				writeJSON(rw, map[string]string{"error": "login required"})
				return
			}
			http.Redirect(rw, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// JSON + 自定义请求头阻止跨站表单提交；不开放 CORS。
func authPost(rw http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		rw.Header().Set("Allow", "POST")
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if r.Header.Get("X-Spider-Auth") != "1" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (w *WebServer) handleLogin(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if w.authenticated(r) {
			http.Redirect(rw, r, "/", http.StatusSeeOther)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(rw, loginUI)
		return
	}
	if !authPost(rw, r) {
		return
	}
	// 全局每分钟最多 30 次尝试，不信任客户端转发的 IP 头。
	w.authMu.Lock()
	now := time.Now()
	if now.Sub(w.loginWindow) >= time.Minute {
		w.loginWindow = now
		w.loginAttempts = 0
	}
	w.loginAttempts++
	allowed := w.loginAttempts <= 30
	w.authMu.Unlock()
	if !allowed {
		rw.Header().Set("Retry-After", "60")
		http.Error(rw, "too many attempts", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(rw, r.Body, 4096)
	var input struct {
		User   string `json:"user"`
		Passwd string `json:"passwd"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&input); err != nil {
		http.Error(rw, "invalid request", http.StatusBadRequest)
		return
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		http.Error(rw, "invalid request", http.StatusBadRequest)
		return
	}
	user, wantUser := sha256.Sum256([]byte(input.User)), sha256.Sum256([]byte(w.authConfig.User))
	password, wantPassword := sha256.Sum256([]byte(input.Passwd)), sha256.Sum256([]byte(w.authConfig.Passwd))
	valid := subtle.ConstantTimeCompare(user[:], wantUser[:]) & subtle.ConstantTimeCompare(password[:], wantPassword[:])
	if valid != 1 || w.authConfig.User == "" || w.authConfig.Passwd == "" {
		http.Error(rw, "invalid username or password", http.StatusUnauthorized)
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		http.Error(rw, "session unavailable", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(random[:])
	expiry := now.Add(sessionTTL)
	w.authMu.Lock()
	for key, end := range w.sessions {
		if !now.Before(end) {
			delete(w.sessions, key)
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		delete(w.sessions, c.Value)
	}
	if len(w.sessions) >= 128 {
		w.authMu.Unlock()
		http.Error(rw, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	w.sessions[token] = expiry
	w.authMu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: int(sessionTTL.Seconds())})
	rw.WriteHeader(http.StatusNoContent)
}

func (w *WebServer) handleLogout(rw http.ResponseWriter, r *http.Request) {
	if !authPost(rw, r) {
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		w.authMu.Lock()
		delete(w.sessions, c.Value)
		w.authMu.Unlock()
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	rw.WriteHeader(http.StatusNoContent)
}
