package stat

import (
    "time"
)

const (
    historyHourCount  = 24
    historyDayCount   = 7
    historyMonthCount = 12
)

func hourTimestamp(t time.Time) int64 {
    return t.Truncate(time.Hour).Unix()
}

func dayTimestamp(t time.Time) int64 {
    return time.Date(
        t.Year(), t.Month(), t.Day(),
        0, 0, 0, 0, t.Location(),
    ).Unix()
}

func monthTimestamp(t time.Time) int64 {
    return time.Date(
        t.Year(), t.Month(), 1,
        0, 0, 0, 0, t.Location(),
    ).Unix()
}

func addSnapshot(
    dst map[int64]TrafficSnapshot,
    ts int64,
    up, down int64,
) {
    v := dst[ts]
    v.Upload += up
    v.Download += down
    dst[ts] = v
}

func cleanupBuckets(
    all map[string]map[int64]TrafficSnapshot,
    cutoff int64,
) {
    for _, buckets := range all {
        for ts := range buckets {
            if ts < cutoff {
                delete(buckets, ts)
            }
        }
    }
}