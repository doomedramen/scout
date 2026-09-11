//go:build darwin || linux

package collector

import (
	"syscall"
	"time"
)

func filesystemMetrics(root string, now time.Time) ([]Metric, error) {
	var info syscall.Statfs_t
	if err := syscall.Statfs(root, &info); err != nil {
		return nil, err
	}
	total := uint64(info.Blocks) * uint64(info.Bsize)
	free := uint64(info.Bavail) * uint64(info.Bsize)
	used := total - free
	percent := float64(0)
	if total > 0 {
		percent = 100 * float64(used) / float64(total)
	}
	return []Metric{{EntityID: "root", Metric: "filesystem.used", Value: floatPtr(float64(used)), Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "root", Metric: "filesystem.capacity", Value: floatPtr(float64(total)), Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "root", Metric: "filesystem.used_percent", Value: &percent, Availability: "current", Unit: "percent", ObservedAt: now}}, nil
}
func floatPtr(value float64) *float64 { return &value }
