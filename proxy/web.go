package proxy

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	com "wangjui.com/enproxy/common"
)

type WebServer struct {
	addr     string
	traffic  *TrafficManager
	inbounds []InboundConfig
	accounts []AccountConfig
	server   *http.Server
}

func NewWebServer(addr string, traffic *TrafficManager, inbounds []InboundConfig, accounts []AccountConfig) *WebServer {
	return &WebServer{addr: addr, traffic: traffic, inbounds: inbounds, accounts: accounts}
}

func (w *WebServer) Run() {
	if err := w.Start(); err != nil && err != http.ErrServerClosed {
		com.Log.Fatalf("web server failed: %v", err)
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
	log.Printf("Web dashboard listening on http://%s", w.addr)
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
		log.Printf("encode JSON failed: %v", err)
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
	writeJSON(rw, map[string]interface{}{"inbounds": w.inbounds, "accounts": w.accounts})
}
