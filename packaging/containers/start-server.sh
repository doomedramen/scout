#!/bin/sh
set -eu

/usr/local/bin/scout-server &
server_pid=$!

node /app/web/apps/web/server.js &
web_pid=$!

stop() {
  kill -TERM "$web_pid" "$server_pid" 2>/dev/null || true
}

trap stop INT TERM
wait "$web_pid"
status=$?
stop
wait "$server_pid" 2>/dev/null || true
exit "$status"
