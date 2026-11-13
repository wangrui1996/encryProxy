package stat

import (
    "sync"
    "sync/atomic"
    "time"
)

type Manager struct {
    mu        sync.RWMutex
    startTime time.Time

    ports map[string]*Traffic
    users map[string]*Traffic

    hourlyPorts  map[string]map[int64]TrafficSnapshot
    dailyPorts   map[string]map[int64]TrafficSnapshot
    monthlyPorts map[string]map[int64]TrafficSnapshot

    hourlyUsers  map[string]map[int64]TrafficSnapshot
    dailyUsers   map[string]map[int64]TrafficSnapshot
    monthlyUsers map[string]map[int64]TrafficSnapshot

    lastSample atomic.Int64

    // 后台采样任务生命周期。
    lifecycleMu sync.Mutex
    cancel      func()
    started     bool
    closed      bool
    wg          sync.WaitGroup
}

func NewManager() *Manager {
    return &Manager{
        startTime: time.Now(),

        ports: make(map[string]*Traffic),
        users: make(map[string]*Traffic),

        hourlyPorts:  make(map[string]map[int64]TrafficSnapshot),
        dailyPorts:   make(map[string]map[int64]TrafficSnapshot),
        monthlyPorts: make(map[string]map[int64]TrafficSnapshot),

        hourlyUsers:  make(map[string]map[int64]TrafficSnapshot),
        dailyUsers:   make(map[string]map[int64]TrafficSnapshot),
        monthlyUsers: make(map[string]map[int64]TrafficSnapshot),
    }
}

// Register 注册端口统计对象。
func (m *Manager) Register(listen string) *Traffic {
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

// RegisterUser 注册用户统计对象。
// 未认证连接使用 anonymous。
func (m *Manager) RegisterUser(name string) *Traffic {
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

// Start 启动后台采样任务。
// 可以重复调用，实际只启动一个采样 goroutine。
func (m *Manager) Start() {
    m.lifecycleMu.Lock()
    defer m.lifecycleMu.Unlock()

    if m.started || m.closed {
        return
    }

    var cancelCh = make(chan struct{})
    m.cancel = func() {
        close(cancelCh)
    }

    m.started = true
    m.wg.Add(1)

    go func() {
        defer m.wg.Done()

        ticker := time.NewTicker(time.Second)
        defer ticker.Stop()

        for {
            select {
            case now := <-ticker.C:
                m.collect(now)

            case <-cancelCh:
                return
            }
        }
    }()
}

// Close 停止后台采样任务。
// 可以重复调用。
func (m *Manager) Close() {
    m.lifecycleMu.Lock()

    if m.closed {
        m.lifecycleMu.Unlock()
        return
    }

    m.closed = true

    if m.cancel != nil {
        m.cancel()
        m.cancel = nil
    }

    m.lifecycleMu.Unlock()

    m.wg.Wait()
}

func (m *Manager) collect(now time.Time) {
    m.mu.Lock()
    defer m.mu.Unlock()

    hour := hourTimestamp(now)
    day := dayTimestamp(now)
    month := monthTimestamp(now)

    collectOne := func(
        name string,
        t *Traffic,
        hourly, daily, monthly map[string]map[int64]TrafficSnapshot,
    ) {
        up := t.currentUpload.Swap(0)
        down := t.currentDownload.Swap(0)

        t.lastUpload.Store(up)
        t.lastDownload.Store(down)

        addSnapshot(hourly[name], hour, up, down)
        addSnapshot(daily[name], day, up, down)
        addSnapshot(monthly[name], month, up, down)
    }

    for name, t := range m.ports {
        collectOne(
            name, t,
            m.hourlyPorts,
            m.dailyPorts,
            m.monthlyPorts,
        )
    }

    for name, t := range m.users {
        collectOne(
            name, t,
            m.hourlyUsers,
            m.dailyUsers,
            m.monthlyUsers,
        )
    }

    m.lastSample.Store(now.Unix())
    m.cleanupHistory(now)
}

func (m *Manager) cleanupHistory(now time.Time) {
    hourCutoff := hourTimestamp(
        now.Add(-time.Duration(historyHourCount-1) * time.Hour),
    )

    dayCutoff := dayTimestamp(
        now.AddDate(0, 0, -(historyDayCount-1)),
    )

    monthCutoff := monthTimestamp(
        now.AddDate(0, -(historyMonthCount-1), 0),
    )

    cleanupBuckets(m.hourlyPorts, hourCutoff)
    cleanupBuckets(m.hourlyUsers, hourCutoff)

    cleanupBuckets(m.dailyPorts, dayCutoff)
    cleanupBuckets(m.dailyUsers, dayCutoff)

    cleanupBuckets(m.monthlyPorts, monthCutoff)
    cleanupBuckets(m.monthlyUsers, monthCutoff)
}

// GetSummary 获取累计流量。
func (m *Manager) GetSummary() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()

    ports := make(map[string]TrafficSnapshot)
    users := make(map[string]TrafficSnapshot)

    var total TrafficSnapshot

    for name, t := range m.ports {
        v := TrafficSnapshot{
            Upload:   t.totalUpload.Load(),
            Download: t.totalDownload.Load(),
        }

        ports[name] = v
        total.Upload += v.Upload
        total.Download += v.Download
    }

    for name, t := range m.users {
        users[name] = TrafficSnapshot{
            Upload:   t.totalUpload.Load(),
            Download: t.totalDownload.Load(),
        }
    }

    return map[string]interface{}{
        "start_time": m.startTime,
        "total":      total,
        "ports":      ports,
        "users":      users,
    }
}

// GetRealtime 获取最近一次采样的流量。
func (m *Manager) GetRealtime() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()

    ports := make(map[string]TrafficSnapshot)
    users := make(map[string]TrafficSnapshot)

    var total TrafficSnapshot

    for name, t := range m.ports {
        v := TrafficSnapshot{
            Upload:   t.lastUpload.Load(),
            Download: t.lastDownload.Load(),
        }

        ports[name] = v
        total.Upload += v.Upload
        total.Download += v.Download
    }

    for name, t := range m.users {
        users[name] = TrafficSnapshot{
            Upload:   t.lastUpload.Load(),
            Download: t.lastDownload.Load(),
        }
    }

    return map[string]interface{}{
        "timestamp": m.lastSample.Load(),
        "total":     total,
        "ports":     ports,
        "users":     users,
    }
}

func (m *Manager) GetHourlyHistory() TrafficHistory {
    return m.getHistory("hour", historyHourCount, time.Now())
}

func (m *Manager) GetDailyHistory() TrafficHistory {
    return m.getHistory("day", historyDayCount, time.Now())
}

func (m *Manager) GetMonthlyHistory() TrafficHistory {
    return m.getHistory("month", historyMonthCount, time.Now())
}

func (m *Manager) getHistory(
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
        var t time.Time

        switch interval {
        case "hour":
            t = now.Truncate(time.Hour).
                Add(-time.Duration(i) * time.Hour)

        case "day":
            t = time.Date(
                now.Year(), now.Month(), now.Day(),
                0, 0, 0, 0, now.Location(),
            ).AddDate(0, 0, -i)

        default:
            t = time.Date(
                now.Year(), now.Month(), 1,
                0, 0, 0, 0, now.Location(),
            ).AddDate(0, -i, 0)
        }

        ts := t.Unix()

        bucket := TrafficBucket{
            Timestamp: ts,
            Ports:     make(map[string]TrafficSnapshot),
            Users:     make(map[string]TrafficSnapshot),
        }

        var portHistory, userHistory map[string]map[int64]TrafficSnapshot

        switch interval {
        case "hour":
            portHistory = m.hourlyPorts
            userHistory = m.hourlyUsers

        case "day":
            portHistory = m.dailyPorts
            userHistory = m.dailyUsers

        default:
            portHistory = m.monthlyPorts
            userHistory = m.monthlyUsers
        }

        for name := range m.ports {
            v := portHistory[name][ts]

            bucket.Ports[name] = v
            bucket.Total.Upload += v.Upload
            bucket.Total.Download += v.Download
        }

        for name := range m.users {
            bucket.Users[name] = userHistory[name][ts]
        }

        result.Buckets = append(result.Buckets, bucket)
    }

    return result
}