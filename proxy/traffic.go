package proxy

import (
    "net"
    "sync"
    "sync/atomic"
    "time"
)

const (
    historyHourCount  = 24
    historyDayCount   = 7
    historyMonthCount = 12
)

type TrafficSnapshot struct {
    Upload   int64 `json:"upload"`
    Download int64 `json:"download"`
}

type TrafficBucket struct {
    Timestamp int64                      `json:"timestamp"`
    Total     TrafficSnapshot            `json:"total"`
    Ports     map[string]TrafficSnapshot `json:"ports"`
    Users     map[string]TrafficSnapshot `json:"users"`
}

type TrafficHistory struct {
    Interval    string          `json:"interval"`
    GeneratedAt int64           `json:"generated_at"`
    Buckets     []TrafficBucket `json:"buckets"`
}

type TrafficManager struct {
    mu           sync.RWMutex
    startTime    time.Time
    ports        map[string]*Traffic
    users        map[string]*Traffic
    hourlyPorts  map[string]map[int64]TrafficSnapshot
    dailyPorts   map[string]map[int64]TrafficSnapshot
    monthlyPorts map[string]map[int64]TrafficSnapshot
    hourlyUsers  map[string]map[int64]TrafficSnapshot
    dailyUsers   map[string]map[int64]TrafficSnapshot
    monthlyUsers map[string]map[int64]TrafficSnapshot
    lastSample   atomic.Int64
}

type Traffic struct {
    name            string
    totalUpload     atomic.Int64
    totalDownload   atomic.Int64
    currentUpload   atomic.Int64
    currentDownload atomic.Int64
    lastUpload      atomic.Int64
    lastDownload    atomic.Int64
}

type TrafficConn struct {
    net.Conn
    portTraffic *Traffic
    userTraffic *Traffic
}

func NewTrafficManager() *TrafficManager {
    return &TrafficManager{
        startTime:    time.Now(),
        ports:        make(map[string]*Traffic),
        users:        make(map[string]*Traffic),
        hourlyPorts:  make(map[string]map[int64]TrafficSnapshot),
        dailyPorts:   make(map[string]map[int64]TrafficSnapshot),
        monthlyPorts: make(map[string]map[int64]TrafficSnapshot),
        hourlyUsers:  make(map[string]map[int64]TrafficSnapshot),
        dailyUsers:   make(map[string]map[int64]TrafficSnapshot),
        monthlyUsers: make(map[string]map[int64]TrafficSnapshot),
    }
}

func newTraffic(name string) *Traffic { return &Traffic{name: name} }

func (m *TrafficManager) Register(listen string) *Traffic {
    m.mu.Lock()
    defer m.mu.Unlock()
    if t := m.ports[listen]; t != nil {
        return t
    }
    t := newTraffic(listen)
    m.ports[listen] = t
    m.hourlyPorts[listen] = make(map[int64]TrafficSnapshot)
    m.dailyPorts[listen] = make(map[int64]TrafficSnapshot)
    m.monthlyPorts[listen] = make(map[int64]TrafficSnapshot)
    return t
}

// RegisterUser 注册统计用户。未认证连接使用 anonymous。
func (m *TrafficManager) RegisterUser(name string) *Traffic {
    if name == "" {
        name = "anonymous"
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    if t := m.users[name]; t != nil {
        return t
    }
    t := newTraffic(name)
    m.users[name] = t
    m.hourlyUsers[name] = make(map[int64]TrafficSnapshot)
    m.dailyUsers[name] = make(map[int64]TrafficSnapshot)
    m.monthlyUsers[name] = make(map[int64]TrafficSnapshot)
    return t
}

func NewTrafficConn(conn net.Conn, portTraffic, userTraffic *Traffic) *TrafficConn {
    return &TrafficConn{Conn: conn, portTraffic: portTraffic, userTraffic: userTraffic}
}

func addTraffic(t *Traffic, upload, download int64) {
    if t == nil {
        return
    }
    if upload > 0 {
        t.totalUpload.Add(upload)
        t.currentUpload.Add(upload)
    }
    if download > 0 {
        t.totalDownload.Add(download)
        t.currentDownload.Add(download)
    }
}

// Read: 下游 -> 代理，计为上传。
func (c *TrafficConn) Read(b []byte) (int, error) {
    n, err := c.Conn.Read(b)
    if n > 0 {
        addTraffic(c.portTraffic, int64(n), 0)
        addTraffic(c.userTraffic, int64(n), 0)
    }
    return n, err
}

// Write: 代理 -> 下游，计为下载。
func (c *TrafficConn) Write(b []byte) (int, error) {
    n, err := c.Conn.Write(b)
    if n > 0 {
        addTraffic(c.portTraffic, 0, int64(n))
        addTraffic(c.userTraffic, 0, int64(n))
    }
    return n, err
}

func (c *TrafficConn) CloseWrite() error {
    cw, ok := c.Conn.(interface{ CloseWrite() error })
    if !ok {
        return net.ErrClosed
    }
    return cw.CloseWrite()
}

func (m *TrafficManager) Start() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()
    for now := range ticker.C {
        m.collect(now)
    }
}

func hourTimestamp(t time.Time) int64 { return t.Truncate(time.Hour).Unix() }
func dayTimestamp(t time.Time) int64 {
    return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Unix()
}
func monthTimestamp(t time.Time) int64 {
    return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Unix()
}

func addSnapshot(dst map[int64]TrafficSnapshot, ts int64, up, down int64) {
    v := dst[ts]
    v.Upload += up
    v.Download += down
    dst[ts] = v
}

func (m *TrafficManager) collect(now time.Time) {
    m.mu.Lock()
    defer m.mu.Unlock()

    hour, day, month := hourTimestamp(now), dayTimestamp(now), monthTimestamp(now)
    collectOne := func(name string, t *Traffic,
        hourly, daily, monthly map[string]map[int64]TrafficSnapshot) {
        up := t.currentUpload.Swap(0)
        down := t.currentDownload.Swap(0)
        t.lastUpload.Store(up)
        t.lastDownload.Store(down)
        addSnapshot(hourly[name], hour, up, down)
        addSnapshot(daily[name], day, up, down)
        addSnapshot(monthly[name], month, up, down)
    }
    for name, t := range m.ports {
        collectOne(name, t, m.hourlyPorts, m.dailyPorts, m.monthlyPorts)
    }
    for name, t := range m.users {
        collectOne(name, t, m.hourlyUsers, m.dailyUsers, m.monthlyUsers)
    }
    m.lastSample.Store(now.Unix())
    m.cleanupHistory(now)
}

func cleanupBuckets(all map[string]map[int64]TrafficSnapshot, cutoff int64) {
    for _, buckets := range all {
        for ts := range buckets {
            if ts < cutoff {
                delete(buckets, ts)
            }
        }
    }
}

func (m *TrafficManager) cleanupHistory(now time.Time) {
    hourCutoff := hourTimestamp(now.Add(-time.Duration(historyHourCount-1) * time.Hour))
    dayCutoff := dayTimestamp(now.AddDate(0, 0, -(historyDayCount - 1)))
    monthCutoff := monthTimestamp(now.AddDate(0, -(historyMonthCount - 1), 0))
    cleanupBuckets(m.hourlyPorts, hourCutoff)
    cleanupBuckets(m.hourlyUsers, hourCutoff)
    cleanupBuckets(m.dailyPorts, dayCutoff)
    cleanupBuckets(m.dailyUsers, dayCutoff)
    cleanupBuckets(m.monthlyPorts, monthCutoff)
    cleanupBuckets(m.monthlyUsers, monthCutoff)
}

func (m *TrafficManager) GetSummary() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()
    ports, users := make(map[string]TrafficSnapshot), make(map[string]TrafficSnapshot)
    var total TrafficSnapshot
    for name, t := range m.ports {
        v := TrafficSnapshot{Upload: t.totalUpload.Load(), Download: t.totalDownload.Load()}
        ports[name] = v
        total.Upload += v.Upload
        total.Download += v.Download
    }
    for name, t := range m.users {
        users[name] = TrafficSnapshot{Upload: t.totalUpload.Load(), Download: t.totalDownload.Load()}
    }
    return map[string]interface{}{"start_time": m.startTime, "total": total, "ports": ports, "users": users}
}

func (m *TrafficManager) GetRealtime() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()
    ports, users := make(map[string]TrafficSnapshot), make(map[string]TrafficSnapshot)
    var total TrafficSnapshot
    for name, t := range m.ports {
        v := TrafficSnapshot{Upload: t.lastUpload.Load(), Download: t.lastDownload.Load()}
        ports[name] = v
        total.Upload += v.Upload
        total.Download += v.Download
    }
    for name, t := range m.users {
        users[name] = TrafficSnapshot{Upload: t.lastUpload.Load(), Download: t.lastDownload.Load()}
    }
    return map[string]interface{}{"timestamp": m.lastSample.Load(), "total": total, "ports": ports, "users": users}
}

func (m *TrafficManager) GetHourlyHistory() TrafficHistory {
    return m.getHistory("hour", historyHourCount, time.Now())
}
func (m *TrafficManager) GetDailyHistory() TrafficHistory {
    return m.getHistory("day", historyDayCount, time.Now())
}
func (m *TrafficManager) GetMonthlyHistory() TrafficHistory {
    return m.getHistory("month", historyMonthCount, time.Now())
}

func (m *TrafficManager) getHistory(interval string, count int, now time.Time) TrafficHistory {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := TrafficHistory{Interval: interval, GeneratedAt: now.Unix(), Buckets: make([]TrafficBucket, 0, count)}
    for i := count - 1; i >= 0; i-- {
        var t time.Time
        switch interval {
        case "hour":
            t = now.Truncate(time.Hour).Add(-time.Duration(i) * time.Hour)
        case "day":
            t = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -i)
        default:
            t = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -i, 0)
        }
        ts := t.Unix()
        b := TrafficBucket{Timestamp: ts, Ports: make(map[string]TrafficSnapshot), Users: make(map[string]TrafficSnapshot)}
        var portHistory, userHistory map[string]map[int64]TrafficSnapshot
        switch interval {
        case "hour":
            portHistory, userHistory = m.hourlyPorts, m.hourlyUsers
        case "day":
            portHistory, userHistory = m.dailyPorts, m.dailyUsers
        default:
            portHistory, userHistory = m.monthlyPorts, m.monthlyUsers
        }
        for name := range m.ports {
            v := portHistory[name][ts]
            b.Ports[name] = v
            b.Total.Upload += v.Upload
            b.Total.Download += v.Download
        }
        for name := range m.users {
            b.Users[name] = userHistory[name][ts]
        }
        result.Buckets = append(result.Buckets, b)
    }
    return result
}
