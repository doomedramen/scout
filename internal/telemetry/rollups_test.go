package telemetry

import (
	"math"
	"sort"
	"testing"
	"time"
)

const (
	fixtureFiveMinute = 5 * time.Minute
	fixtureHourly     = time.Hour
)

type rollupFixturePoint struct {
	observedAt   time.Time
	value        *float64
	availability string
	interval     time.Duration
}

type rollupFixtureBucket struct {
	start         time.Time
	count         int64
	sum           float64
	min           *float64
	max           *float64
	expectedCount int64
	covered       time.Duration
	bucket        time.Duration
	partial       bool
}

type rollupFixtureInterval struct {
	start time.Time
	end   time.Time
}

func fixtureValue(value float64) *float64 {
	return &value
}

func fixtureBucketStart(at time.Time, resolution time.Duration) time.Time {
	return at.UTC().Truncate(resolution)
}

func fixtureAggregate(points []rollupFixturePoint, start time.Time, resolution time.Duration) rollupFixtureBucket {
	start = start.UTC()
	end := start.Add(resolution)
	items := append([]rollupFixturePoint(nil), points...)
	sort.Slice(items, func(left, right int) bool { return items[left].observedAt.Before(items[right].observedAt) })
	result := rollupFixtureBucket{start: start, bucket: resolution}
	intervals := []rollupFixtureInterval{}
	var configuredInterval time.Duration
	for index, point := range items {
		if point.observedAt.Before(start) || !point.observedAt.Before(end) || point.interval <= 0 {
			continue
		}
		if configuredInterval == 0 {
			configuredInterval = point.interval
		}
		if point.availability != "current" || point.value == nil {
			continue
		}
		result.count++
		result.sum += *point.value
		if result.min == nil || *point.value < *result.min {
			result.min = fixtureValue(*point.value)
		}
		if result.max == nil || *point.value > *result.max {
			result.max = fixtureValue(*point.value)
		}
		coverageEnd := point.observedAt.Add(point.interval)
		if index+1 < len(items) && items[index+1].observedAt.After(point.observedAt) && items[index+1].observedAt.Before(coverageEnd) {
			coverageEnd = items[index+1].observedAt
		}
		coverageStart := point.observedAt
		if coverageStart.Before(start) {
			coverageStart = start
		}
		if coverageEnd.After(end) {
			coverageEnd = end
		}
		if coverageEnd.After(coverageStart) {
			intervals = append(intervals, rollupFixtureInterval{start: coverageStart, end: coverageEnd})
		}
	}
	if configuredInterval > 0 {
		result.expectedCount = int64(math.Ceil(float64(resolution) / float64(configuredInterval)))
	}
	result.covered = fixtureIntervalUnion(intervals)
	return result
}

func fixtureIntervalUnion(intervals []rollupFixtureInterval) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(left, right int) bool { return intervals[left].start.Before(intervals[right].start) })
	covered := time.Duration(0)
	start, end := intervals[0].start, intervals[0].end
	for _, interval := range intervals[1:] {
		if !interval.start.After(end) {
			if interval.end.After(end) {
				end = interval.end
			}
			continue
		}
		covered += end.Sub(start)
		start, end = interval.start, interval.end
	}
	return covered + end.Sub(start)
}

func fixtureMean(bucket rollupFixtureBucket) *float64 {
	if bucket.count == 0 {
		return nil
	}
	mean := bucket.sum / float64(bucket.count)
	return &mean
}

func fixtureCombine(buckets ...rollupFixtureBucket) rollupFixtureBucket {
	result := rollupFixtureBucket{}
	for index, bucket := range buckets {
		if index == 0 {
			result.start = bucket.start
		}
		result.count += bucket.count
		result.sum += bucket.sum
		result.expectedCount += bucket.expectedCount
		result.covered += bucket.covered
		result.bucket += bucket.bucket
		result.partial = result.partial || bucket.partial
		if bucket.min != nil && (result.min == nil || *bucket.min < *result.min) {
			result.min = fixtureValue(*bucket.min)
		}
		if bucket.max != nil && (result.max == nil || *bucket.max > *result.max) {
			result.max = fixtureValue(*bucket.max)
		}
	}
	return result
}

func fixtureBoundaryBuckets(from, to time.Time, resolution time.Duration) []rollupFixtureBucket {
	from, to = from.UTC(), to.UTC()
	result := []rollupFixtureBucket{}
	for start := fixtureBucketStart(from, resolution); start.Before(to); start = start.Add(resolution) {
		end := start.Add(resolution)
		left, right := from, to
		if left.Before(start) {
			left = start
		}
		if right.After(end) {
			right = end
		}
		result = append(result, rollupFixtureBucket{
			start:   start,
			bucket:  right.Sub(left),
			partial: left.After(start) || right.Before(end),
		})
	}
	return result
}

func fixtureGroup(buckets []rollupFixtureBucket, maxPoints int) []rollupFixtureBucket {
	if maxPoints < 2 || maxPoints > 600 {
		return nil
	}
	step := (len(buckets) + maxPoints - 1) / maxPoints
	result := make([]rollupFixtureBucket, 0, maxPoints)
	for start := 0; start < len(buckets); start += step {
		end := start + step
		if end > len(buckets) {
			end = len(buckets)
		}
		result = append(result, fixtureCombine(buckets[start:end]...))
	}
	return result
}

func fixtureRetained(observedAt, now time.Time, days int) bool {
	return !observedAt.Before(now.Add(-time.Duration(days) * 24 * time.Hour))
}

func TestRollupFixtureUsesUTCAlignedHalfOpenBuckets(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 4, 59, 123_000_000, time.FixedZone("BST", 3600))
	if got, want := fixtureBucketStart(at, fixtureFiveMinute), time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("bucket start = %s, want %s", got, want)
	}
	next := at.Add(time.Second)
	if got, want := fixtureBucketStart(next, fixtureFiveMinute), time.Date(2026, 9, 11, 11, 5, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("boundary sample bucket start = %s, want %s", got, want)
	}
}

func TestRollupFixtureTracksWeightedStatisticsAndMissingCoverage(t *testing.T) {
	start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	bucket := fixtureAggregate([]rollupFixturePoint{
		{observedAt: start, value: fixtureValue(10), availability: "current", interval: time.Minute},
		{observedAt: start.Add(time.Minute), value: fixtureValue(20), availability: "current", interval: time.Minute},
		{observedAt: start.Add(3 * time.Minute), value: fixtureValue(40), availability: "current", interval: time.Minute},
	}, start, fixtureFiveMinute)
	if bucket.count != 3 || bucket.expectedCount != 5 || bucket.covered != 3*time.Minute {
		t.Fatalf("unexpected count/expected/coverage: %+v", bucket)
	}
	if *bucket.min != 10 || *bucket.max != 40 || *fixtureMean(bucket) != 70.0/3.0 {
		t.Fatalf("unexpected statistics: %+v", bucket)
	}
	slowerCadence := fixtureAggregate([]rollupFixturePoint{
		{observedAt: start, value: fixtureValue(10), availability: "current", interval: 2 * time.Minute},
		{observedAt: start.Add(2 * time.Minute), value: fixtureValue(20), availability: "current", interval: 2 * time.Minute},
	}, start, fixtureFiveMinute)
	if slowerCadence.expectedCount != 3 || slowerCadence.covered != 4*time.Minute {
		t.Fatalf("cadence change was not represented: %+v", slowerCadence)
	}
	if empty := fixtureAggregate([]rollupFixturePoint{{observedAt: start, availability: "unavailable", interval: time.Minute}}, start, fixtureFiveMinute); empty.count != 0 || empty.min != nil || empty.max != nil || fixtureMean(empty) != nil {
		t.Fatalf("unavailable sample contributed to statistics: %+v", empty)
	}

	left := rollupFixtureBucket{count: 2, sum: 10, min: fixtureValue(2), max: fixtureValue(8), bucket: fixtureHourly}
	right := rollupFixtureBucket{count: 8, sum: 80, min: fixtureValue(1), max: fixtureValue(20), bucket: fixtureHourly}
	combined := fixtureCombine(left, right)
	if combined.count != 10 || *fixtureMean(combined) != 9 || *combined.min != 1 || *combined.max != 20 {
		t.Fatalf("coarser bucket did not use weighted statistics: %+v", combined)
	}
}

func TestRollupFixtureClipsPartialBoundariesAndUnionsCoverage(t *testing.T) {
	start := time.Date(2026, 9, 11, 12, 0, 30, 0, time.UTC)
	end := time.Date(2026, 9, 11, 12, 10, 15, 0, time.UTC)
	buckets := fixtureBoundaryBuckets(start, end, fixtureFiveMinute)
	if len(buckets) != 3 || !buckets[0].partial || buckets[0].bucket != 270*time.Second || buckets[1].partial || buckets[1].bucket != fixtureFiveMinute || !buckets[2].partial || buckets[2].bucket != 15*time.Second {
		t.Fatalf("unexpected clipped boundary buckets: %+v", buckets)
	}
	coverage := fixtureIntervalUnion([]rollupFixtureInterval{
		{start: start, end: start.Add(2 * time.Minute)},
		{start: start.Add(time.Minute), end: start.Add(3 * time.Minute)},
	})
	if coverage != 3*time.Minute {
		t.Fatalf("overlapping coverage was double-counted: %s", coverage)
	}
}

func TestRollupFixtureLateDataRecomputesOneBucket(t *testing.T) {
	start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	points := []rollupFixturePoint{
		{observedAt: start, value: fixtureValue(10), availability: "current", interval: time.Minute},
		{observedAt: start.Add(time.Minute), value: fixtureValue(20), availability: "current", interval: time.Minute},
	}
	first := fixtureAggregate(points, start, fixtureFiveMinute)
	points = append(points, rollupFixturePoint{observedAt: start.Add(2 * time.Minute), value: fixtureValue(90), availability: "current", interval: time.Minute})
	late := fixtureAggregate(points, start, fixtureFiveMinute)
	if first.count != 2 || late.count != 3 || *late.max != 90 || len(map[time.Time]rollupFixtureBucket{start: late}) != 1 {
		t.Fatalf("late sample did not replace the same bucket: first=%+v late=%+v", first, late)
	}
}

func TestRollupFixtureRetentionAndYearRangeStayBounded(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if !fixtureRetained(now.Add(-30*24*time.Hour), now, 30) || fixtureRetained(now.Add(-(30*24*time.Hour+time.Second)), now, 30) {
		t.Fatal("raw retention boundary is not exact")
	}
	if !fixtureRetained(now.Add(-90*24*time.Hour), now, 90) || fixtureRetained(now.Add(-(90*24*time.Hour+time.Second)), now, 90) {
		t.Fatal("five-minute retention boundary is not exact")
	}
	if !fixtureRetained(now.Add(-365*24*time.Hour), now, 365) || fixtureRetained(now.Add(-(365*24*time.Hour+time.Second)), now, 365) {
		t.Fatal("hourly retention boundary is not exact")
	}

	yearStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	hourly := make([]rollupFixtureBucket, 0, 8760)
	for index := 0; index < 8760; index++ {
		value := float64(index % 100)
		if index == 4321 {
			value = 999
		}
		hourly = append(hourly, rollupFixtureBucket{start: yearStart.Add(time.Duration(index) * fixtureHourly), count: 1, sum: value, min: fixtureValue(value), max: fixtureValue(value), expectedCount: 1, covered: fixtureHourly, bucket: fixtureHourly})
	}
	grouped := fixtureGroup(hourly, 600)
	if len(grouped) > 600 || len(grouped) != 584 {
		t.Fatalf("year range grouping returned %d points", len(grouped))
	}
	if grouped[0].start != yearStart || grouped[len(grouped)-1].start.Add(grouped[len(grouped)-1].bucket) != yearStart.Add(8760*fixtureHourly) {
		t.Fatalf("grouping silently truncated year range: first=%+v last=%+v", grouped[0], grouped[len(grouped)-1])
	}
	spikeSeen := false
	for _, bucket := range grouped {
		if bucket.max != nil && *bucket.max == 999 {
			spikeSeen = true
			break
		}
	}
	if !spikeSeen {
		t.Fatal("grouping lost the known hourly spike")
	}
	if fixtureGroup(hourly, 1) != nil || fixtureGroup(hourly, 601) != nil {
		t.Fatal("invalid point limits were accepted")
	}
}
