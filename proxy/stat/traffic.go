package stat

import "sync/atomic"

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

// Traffic 表示一个端口或用户的流量统计对象。
// 计数器只能通过 addTraffic 更新。
type Traffic struct {
    name            string
    totalUpload     atomic.Int64
    totalDownload   atomic.Int64
    currentUpload   atomic.Int64
    currentDownload atomic.Int64
    lastUpload      atomic.Int64
    lastDownload    atomic.Int64
}

func newTraffic(name string) *Traffic {
    return &Traffic{name: name}
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