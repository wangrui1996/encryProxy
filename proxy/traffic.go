
package proxy

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// 保留最近24小时的数据
	historyHourCount = 24

	// 保留最近30天的数据
	historyDayCount = 30
)

// TrafficSnapshot 流量快照，单位 Byte。
type TrafficSnapshot struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

// TrafficBucket 历史流量时间桶。
type TrafficBucket struct {
	Timestamp int64           `json:"timestamp"`
	Total     TrafficSnapshot `json:"total"`
	Ports     map[string]TrafficSnapshot `json:"ports"`
}

// TrafficHistory 历史统计结果。
type TrafficHistory struct {
	Interval   string          `json:"interval"`
	GeneratedAt int64          `json:"generated_at"`
	Buckets    []TrafficBucket `json:"buckets"`
}

// TrafficManager 管理所有端口的流量统计。
type TrafficManager struct {
	mu sync.RWMutex

	startTime time.Time

	// 所有监听端口
	ports map[string]*Traffic

	// 每小时历史数据
	// key: 监听地址
	// value: 时间戳 -> 流量
	hourly map[string]map[int64]TrafficSnapshot

	// 每天历史数据
	daily map[string]map[int64]TrafficSnapshot

	// 最近一次采样时间
	lastSample atomic.Int64
}

// Traffic 单个监听端口的流量统计。
type Traffic struct {
	listen string

	// 累计流量
	totalUpload   atomic.Int64
	totalDownload atomic.Int64

	// 当前采样周期流量
	currentUpload   atomic.Int64
	currentDownload atomic.Int64

	// 最近一次采样流量
	lastUpload   atomic.Int64
	lastDownload atomic.Int64
}

// TrafficConn 流量统计连接。
type TrafficConn struct {
	net.Conn
	traffic *Traffic
}

// NewTrafficManager 创建流量管理器。
func NewTrafficManager() *TrafficManager {
	return &TrafficManager{
		startTime: time.Now(),

		ports: make(map[string]*Traffic),

		hourly: make(map[string]map[int64]TrafficSnapshot),

		daily: make(map[string]map[int64]TrafficSnapshot),
	}
}

// Register 注册监听端口。
func (m *TrafficManager) Register(listen string) *Traffic {
	m.mu.Lock()
	defer m.mu.Unlock()

	if traffic, ok := m.ports[listen]; ok {
		return traffic
	}

	traffic := &Traffic{
		listen: listen,
	}

	m.ports[listen] = traffic

	m.hourly[listen] = make(map[int64]TrafficSnapshot)

	m.daily[listen] = make(map[int64]TrafficSnapshot)

	return traffic
}

// NewTrafficConn 创建流量统计连接。
func NewTrafficConn(conn net.Conn, traffic *Traffic) *TrafficConn {
	return &TrafficConn{
		Conn:    conn,
		traffic: traffic,
	}
}

// 下游 -> 代理：上传流量。
func (c *TrafficConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)

	if n > 0 {
		c.traffic.totalUpload.Add(int64(n))
		c.traffic.currentUpload.Add(int64(n))
	}

	return n, err
}

// 代理 -> 下游：下载流量。
func (c *TrafficConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)

	if n > 0 {
		c.traffic.totalDownload.Add(int64(n))
		c.traffic.currentDownload.Add(int64(n))
	}

	return n, err
}

// Start 启动每秒流量采样。
// 在主程序中使用 go trafficManager.Start()。
func (m *TrafficManager) Start() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for now := range ticker.C {
		m.collect(now)
	}
}

// 当前小时的起始时间戳。
func hourTimestamp(t time.Time) int64 {
	return t.Truncate(time.Hour).Unix()
}

// 当前日期的零点时间戳。
func dayTimestamp(t time.Time) int64 {
	return time.Date(
		t.Year(),
		t.Month(),
		t.Day(),
		0, 0, 0, 0,
		t.Location(),
	).Unix()
}

// collect 采样所有端口。
func (m *TrafficManager) collect(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hour := hourTimestamp(now)
	day := dayTimestamp(now)

	var totalUpload int64
	var totalDownload int64

	for listen, traffic := range m.ports {
		// 获取本采样周期流量，并清零。
		upload := traffic.currentUpload.Swap(0)
		download := traffic.currentDownload.Swap(0)

		traffic.lastUpload.Store(upload)
		traffic.lastDownload.Store(download)

		totalUpload += upload
		totalDownload += download

		snapshot := TrafficSnapshot{
			Upload:   upload,
			Download: download,
		}

		// 累加到当前小时。
		hourData := m.hourly[listen]

		hourBucket := hourData[hour]
		hourBucket.Upload += upload
		hourBucket.Download += download

		hourData[hour] = hourBucket

		// 累加到当前日期。
		dayData := m.daily[listen]

		dayBucket := dayData[day]
		dayBucket.Upload += upload
		dayBucket.Download += download

		dayData[day] = dayBucket

		_ = snapshot
	}

	m.lastSample.Store(now.Unix())

	// 清理过期历史数据。
	m.cleanupHistory(now)
}

// cleanupHistory 清理历史数据。
func (m *TrafficManager) cleanupHistory(now time.Time) {
	hourCutoff := hourTimestamp(now.Add(-time.Duration(historyHourCount-1) * time.Hour))

	dayCutoff := dayTimestamp(now.AddDate(0, 0, -(historyDayCount - 1)))

	for _, data := range m.hourly {
		for timestamp := range data {
			if timestamp < hourCutoff {
				delete(data, timestamp)
			}
		}
	}

	for _, data := range m.daily {
		for timestamp := range data {
			if timestamp < dayCutoff {
				delete(data, timestamp)
			}
		}
	}
}

// GetSummary 获取累计流量。
func (m *TrafficManager) GetSummary() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ports := make(map[string]TrafficSnapshot)

	var totalUpload int64
	var totalDownload int64

	for listen, traffic := range m.ports {
		upload := traffic.totalUpload.Load()
		download := traffic.totalDownload.Load()

		ports[listen] = TrafficSnapshot{
			Upload:   upload,
			Download: download,
		}

		totalUpload += upload
		totalDownload += download
	}

	return map[string]interface{}{
		"start_time": m.startTime,
		"total": TrafficSnapshot{
			Upload:   totalUpload,
			Download: totalDownload,
		},
		"ports": ports,
	}
}

// GetRealtime 获取最近一次采样流量。
func (m *TrafficManager) GetRealtime() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ports := make(map[string]TrafficSnapshot)

	var totalUpload int64
	var totalDownload int64

	for listen, traffic := range m.ports {
		upload := traffic.lastUpload.Load()
		download := traffic.lastDownload.Load()

		ports[listen] = TrafficSnapshot{
			Upload:   upload,
			Download: download,
		}

		totalUpload += upload
		totalDownload += download
	}

	return map[string]interface{}{
		"timestamp": m.lastSample.Load(),

		"total": TrafficSnapshot{
			Upload:   totalUpload,
			Download: totalDownload,
		},

		"ports": ports,
	}
}

// GetHourlyHistory 获取最近24小时流量。
func (m *TrafficManager) GetHourlyHistory() TrafficHistory {
	now := time.Now()

	return m.getHistory("hour", historyHourCount, now)
}

// GetDailyHistory 获取最近30天流量。
func (m *TrafficManager) GetDailyHistory() TrafficHistory {
	now := time.Now()

	return m.getHistory("day", historyDayCount, now)
}

// getHistory 获取历史流量。
func (m *TrafficManager) getHistory(
	interval string,
	count int,
	now time.Time,
) TrafficHistory {

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := TrafficHistory{
		Interval:    interval,
		GeneratedAt: now.Unix(),
		Buckets:     make([]TrafficBucket, 0, count),
	}

	for i := count - 1; i >= 0; i-- {
		var timestamp int64

		if interval == "hour" {
			timestamp = now.Truncate(time.Hour).
				Add(-time.Duration(i) * time.Hour).
				Unix()
		} else {
			t := time.Date(
				now.Year(),
				now.Month(),
				now.Day(),
				0, 0, 0, 0,
				now.Location(),
			)

			timestamp = t.AddDate(0, 0, -i).Unix()
		}

		bucket := TrafficBucket{
			Timestamp: timestamp,
			Ports:     make(map[string]TrafficSnapshot),
		}

		for listen := range m.ports {
			var snapshot TrafficSnapshot

			if interval == "hour" {
				snapshot = m.hourly[listen][timestamp]
			} else {
				snapshot = m.daily[listen][timestamp]
			}

			bucket.Ports[listen] = snapshot

			bucket.Total.Upload += snapshot.Upload
			bucket.Total.Download += snapshot.Download
		}

		result.Buckets = append(result.Buckets, bucket)
	}

	return result
}
