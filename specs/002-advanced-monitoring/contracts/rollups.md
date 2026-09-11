# Rollup Semantics

## Series and time

Series key includes device, collector, entity, metric, unit and canonical allowed labels. Store both observed and received time. Buckets are half-open UTC intervals. Retention ages refer to observed time; receipt-time raw partitions may only be dropped when every eligible sample in the partition satisfies retention and committed aggregation checks.

Five-minute rows derive from raw samples; hourly rows derive from five-minute rows. Store sum and count to combine averages correctly. Numeric samples with availability=current contribute statistics; missing/unsupported/unavailable samples do not contribute zero. Zero-count buckets have null mean/min/max. Cumulative counters are translated into rates before aggregation; do not average a cumulative counter into throughput. Extrema are of retained sampled values, not unsampled physical peaks.

Coverage uses each valid sample's configured collection interval clipped to bucket boundaries and the next observation; never extend beyond that one interval. Track interval union so duplicate/overlapping coverage is not double-counted. Store coveredSeconds and bucketSeconds. Count and expectedCount additionally explain cadence changes, but coverage uses time, not merely counts. Interval history belongs to the series configuration timeline. A source interval change must be recorded rather than applied retrospectively to all samples. Partial query boundary buckets use only their intersected span and are labeled partial if exact sub-bucket filtering is unavailable.

## Jobs and late data

Every accepted sample marks its five-minute and parent hourly bucket dirty in the same transaction. Worker leases dirty rows, recomputes a bounded bucket from authoritative sources, and updates only its captured generation. A newer dirty generation remains queued. Repeating computation overwrites the same key, never appends another aggregate. Run every minute; process <=1000 buckets or five seconds per transaction cycle. Emit lag, last success and error counts.

Close ordinary buckets after their end plus five minutes, but keep recomputation possible while their source tier exists. Accept late raw data only within raw retention and 001's stricter replay/clock limits if applicable. Late data updates history, not past incidents. No post-expiry raw insertion is permitted merely to fill old rollups. Recompute an hourly row when any child changes; raw deletion requires both tiers' matching generations. Five-minute deletion requires hourly generation committed. Backfill creates work from actual retained ranges only and uses the same algorithm.

## Query and pressure

Follow the control API tier selection. When a selected tier is not built yet, return an explicit aggregation-pending unavailable interval; do not silently read an unbounded year of raw data. Point grouping combines counts/sums/extrema/coverage over selected rows and supplies empty expected intervals. The 600-point cap applies per series after boundary alignment; reject maxPoints<2 or >600. Requested bounds are included in response metadata.

Default volume policy: at 80% of configured telemetry disk budget prioritize retention/aggregation and report warning; at 90% reject new telemetry with existing retryable backpressure semantics while keeping read/control/recovery paths operating. Agent spool remains bounded and reported drops remain visible. Do not delete unaggregated eligible raw data to mask job failure. Operator can explicitly shorten retention; preview the lost history range and audit confirmation before destructive reduction. Budget and actual used bytes include relevant indexes; publish accounting method. No full-database scan per request.
