//go:build !darwin && !linux

package collector

import (
	"errors"
	"time"
)

func filesystemMetrics(string, time.Time) ([]Metric, error) {
	return nil, errors.New("filesystem metrics unsupported on this platform")
}
func floatPtr(value float64) *float64 { return &value }
