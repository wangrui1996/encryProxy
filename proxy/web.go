package proxy

import (
    "context"
    "encoding/json"
    "net/http"
    "time"

//    "wangjui.com/enproxy/proxy/common"
)

//var Logger = common.Log

type WebServer struct {
    addr     string
    traffic  *TrafficManager
    inbounds []InboundConfig
    accounts []AccountConfig
    server   *http.Server
}

type InboundLimitInfo struct {
    Tag          string          `json:"tag"`
    Listen       string          `json:"listen"`
    AuthRequired bool            `json:"auth_required"`
    RateLimit    RateLimitConfig `json:"rate_limit"`
}

type AccountLimitInfo struct {
    Name      string          `json:"name"`
    Enabled   bool            `json:"enabled"`
    RateLimit RateLimitConfig `json:"rate_limit"`
}

func NewWebServer(addr string, traffic *TrafficManager, inbounds []InboundConfig, accounts []AccountConfig) *WebServer {
    return &WebServer{addr: addr, traffic: traffic, inbounds: inbounds, accounts: accounts}
}

func (w *WebServer) Run() {
    if err := w.Start(); err != nil && err != http.ErrServerClosed {
        Logger.Fatalf("web server failed: %v", err)
    }
}

func (w *WebServer) Start() error {
    mux := http.NewServeMux()
    mux.HandleFunc("/", w.handleIndex)
    mux.HandleFunc("/api/traffic/summary", w.handleSummary)
    mux.HandleFunc("/api/traffic/realtime", w.handleRealtime)
    mux.HandleFunc("/api/traffic/history/hourly", w.handleHourly)
    mux.HandleFunc("/api/traffic/history/daily", w.handleDaily)
    mux.HandleFunc("/api/traffic/history/monthly", w.handleMonthly)
    mux.HandleFunc("/api/limits", w.handleLimits)
    w.server = &http.Server{Addr: w.addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
    Logger.Printf("Web dashboard listening on http://%s", w.addr)
    return w.server.ListenAndServe()
}
func (w *WebServer) Shutdown(ctx context.Context) error {
    if w.server == nil {
        return nil
    }
    return w.server.Shutdown(ctx)
}
func writeJSON(rw http.ResponseWriter, data interface{}) {
    rw.Header().Set("Content-Type", "application/json; charset=utf-8")
    if err := json.NewEncoder(rw).Encode(data); err != nil {
        Logger.Printf("encode JSON failed: %v", err)
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
        inbounds = append(inbounds, InboundLimitInfo{
            Tag:          in.Tag,
            Listen:       in.Listen,
            AuthRequired: in.AuthRequired,
            RateLimit:    in.RateLimit,
        })
    }

    accounts := make([]AccountLimitInfo, 0, len(w.accounts))

    for _, account := range w.accounts {
        accounts = append(accounts, AccountLimitInfo{
            Name:      account.Name,
            Enabled:   account.Enabled,
            RateLimit: account.RateLimit,
        })
    }

    writeJSON(rw, map[string]interface{}{
        "inbounds": inbounds,
        "accounts": accounts,
    })
}
