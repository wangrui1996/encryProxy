package stat

import (
	"time"
)

const (
	historyHourCount  = 24
	historyDayCount   = 7
	historyMonthCount = 12
)

func hourStart(t time.Time) time.Time {
	// 不使用 Truncate(time.Hour)：它按 UTC 绝对时长截断，非整小时偏移时区会错位。
	// 用 Add 保留当前时刻的 UTC 偏移，兼容夏令时重复的本地小时。
	return t.Add(-time.Duration(t.Minute())*time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
}

func hourTimestamp(t time.Time) int64 {
	return hourStart(t).Unix()
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

func addSnapshot(dst map[int64]TrafficSnapshot, ts int64, up, down int64) {
	v := dst[ts]
	v.Upload += up
	v.Download += down
	dst[ts] = v
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
