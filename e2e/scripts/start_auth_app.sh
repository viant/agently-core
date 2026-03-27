#!/bin/sh
set -eu

AUTH_SERVER_PORT="${1:?auth server port required}"
AUTH_SERVER_URL="${2:?auth server url required}"
WORKSPACE_PATH="${3:?workspace path required}"
APP_BIN="${4:?app binary path required}"
JWT_PUB="${5:?jwt pub path required}"
JWT_PRIV="${6:?jwt priv path required}"
LOG_FILE="${7:?log file required}"

pids=$(lsof -tiTCP:"${AUTH_SERVER_PORT}" -sTCP:LISTEN || true)
if [ -n "${pids}" ]; then
  kill ${pids} || true
fi
for i in $(seq 1 20); do
  if ! lsof -nP -iTCP:"${AUTH_SERVER_PORT}" -sTCP:LISTEN | grep -q LISTEN; then
    break
  fi
  sleep 0.5
done

(nohup env AGENTLY_WORKSPACE="${WORKSPACE_PATH}" "${APP_BIN}" serve --addr ":${AUTH_SERVER_PORT}" --jwt-pub "${JWT_PUB}" --jwt-priv "${JWT_PRIV}" >"${LOG_FILE}" 2>&1 &)

for i in $(seq 1 40); do
  if curl -fsS "${AUTH_SERVER_URL}/healthz" | grep -q '"status":"ok"'; then
    exit 0
  fi
  sleep 0.5
done

echo "auth server failed to start on ${AUTH_SERVER_URL}/healthz"
tail -n 80 "${LOG_FILE}" || true
exit 1
