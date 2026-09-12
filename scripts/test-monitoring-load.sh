#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

bash -n scripts/test-monitoring-load.sh scripts/test-load.sh
go test ./tests/integration -run TestSyntheticLoad100Devices24Hours -count=1 -v
go test ./internal/store -run 'Test(TelemetryBackpressureIsVisibleAndRecoverable|NotificationDeliveryQueueCapRecordsOverflow)' -count=1 -v

if [[ -n "${SCOUT_TEST_DATABASE_URL:-}" ]]; then
	SCOUT_TEST_DATABASE_URL="$SCOUT_TEST_DATABASE_URL" go test ./tests/integration -run 'TestMonitoring(StorageFailureFixtures|RecoveryPreservesDurableStateAndPreventsNoReplay)' -count=1 -v
else
	echo "SQL disk-pressure and recovery checks are available through scripts/test-integration.sh or SCOUT_TEST_DATABASE_URL."
fi

if [[ "${SCOUT_LOAD_LAB:-0}" == "1" ]]; then
	if [[ "${SCOUT_LOAD_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_LOAD_LAB_CONFIRM=YES only before running a separately provisioned load environment." >&2
		exit 2
	fi
	cat <<'EOF'
The repository workload is synthetic and does not claim a 100-host production
capacity result. A live run must record the 100-host/40-series/50-service
state dataset, raw and tiered row counts, measured PostgreSQL bytes, p95
history latency, evaluation/rollup lag, queue saturation, and backpressure.
Do not reduce the dataset to hide a failing target.
EOF
fi
