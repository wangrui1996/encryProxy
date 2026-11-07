
package proxy

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	com "wangjui.com/enproxy/common"
)

// WebServer Web 管理服务。
type WebServer struct {
	addr    string
	traffic *TrafficManager
	server  *http.Server
}

// NewWebServer 创建 Web 服务。
func NewWebServer(addr string, traffic *TrafficManager) *WebServer {
	return &WebServer{
		addr:    addr,
		traffic: traffic,
	}
}

func (w *WebServer) Run() {

	if err := w.Start(); err != nil && err != http.ErrServerClosed {
		com.Log.Fatalf("web server failed: %v", err)
	}
}

// Start 启动 Web 服务。
func (w *WebServer) Start() error {
	mux := http.NewServeMux()

	// Dashboard 首页。
	mux.HandleFunc("/", w.handleIndex)

	// 累计流量。
	mux.HandleFunc("/api/traffic/summary", w.handleSummary)

	// 实时流量。
	mux.HandleFunc("/api/traffic/realtime", w.handleRealtime)

	// 最近24小时。
	mux.HandleFunc("/api/traffic/history/hourly", w.handleHourly)

	// 最近30天。
	mux.HandleFunc("/api/traffic/history/daily", w.handleDaily)

	w.server = &http.Server{
		Addr:              w.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Web dashboard listening on http://%s", w.addr)

	return w.server.ListenAndServe()
}

// Shutdown 关闭 Web 服务。
func (w *WebServer) Shutdown(ctx context.Context) error {
	if w.server == nil {
		return nil
	}

	return w.server.Shutdown(ctx)
}

// writeJSON 返回 JSON。
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		log.Printf("encode JSON failed: %v", err)
	}
}

// handleIndex Dashboard 首页。
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

// handleSummary 累计流量。
func (w *WebServer) handleSummary(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetSummary())
}

// handleRealtime 实时流量。
func (w *WebServer) handleRealtime(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetRealtime())
}

// handleHourly 最近24小时流量。
func (w *WebServer) handleHourly(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetHourlyHistory())
}

// handleDaily 最近30天流量。
func (w *WebServer) handleDaily(
	rw http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(rw, w.traffic.GetDailyHistory())
}
