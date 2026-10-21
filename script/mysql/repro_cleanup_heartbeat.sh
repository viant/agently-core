#!/usr/bin/env bash
set -euo pipefail

# Only an isolated, disposable, local MySQL is supported. Never reuse a database.
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
core_root="$(cd -- "$script_dir/../.." && pwd)"
repetitions="${1:-1}"
if [[ $# -gt 1 || ! "$repetitions" =~ ^([1-9]|1[0-9]|20)$ ]]; then
    printf 'Usage: bash %s [repetitions: 1-20]\n' "$0" >&2
    exit 2
fi

for dependency in docker openssl lsof; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
        printf 'Missing required command: %s\n' "$dependency" >&2
        exit 1
    fi
done
go_bin="${GO_BIN:-}"
if [[ -z "$go_bin" ]]; then
    go_bin="$(command -v go || true)"
fi
if [[ -z "$go_bin" ]]; then
    # macOS installations may have a cached Go toolchain but no go on PATH.
    for candidate in "${GOPATH:-$HOME/go}"/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.*/bin/go; do
        if [[ -x "$candidate" ]]; then
            go_bin="$candidate"
        fi
    done
fi
if [[ -z "$go_bin" || ! -x "$go_bin" ]]; then
    printf 'Go 1.25.8+ is required; set GO_BIN to its executable.\n' >&2
    exit 1
fi
if [[ -n "${DOCKER_CONTEXT:-}" ]]; then
    docker_endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}' "$DOCKER_CONTEXT")"
elif [[ -n "${DOCKER_HOST:-}" ]]; then
    docker_endpoint="$DOCKER_HOST"
else
    docker_endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}')"
fi
case "$docker_endpoint" in
    unix://*) ;;
    *) printf 'Refusing non-local Docker endpoint: %s\n' "$docker_endpoint" >&2; exit 1 ;;
esac
# Pin all subsequent calls to the validated endpoint, even when DOCKER_CONTEXT
# (which takes precedence over DOCKER_HOST) was set by the caller.
export DOCKER_HOST="$docker_endpoint"
unset DOCKER_CONTEXT
docker info >/dev/null
if lsof -nP -iTCP:3308 -sTCP:LISTEN >/dev/null 2>&1; then
    printf 'Port 3308 is occupied. Nothing was changed; do not stop an unrelated MySQL.\n' >&2
    exit 1
fi

run_token="$(openssl rand -hex 12)"
repro_password="$(openssl rand -hex 24)"
container_name="agently-cleanup-heartbeat-$run_token"
container_id=""
cleanup() {
    status=$?
    if [[ -n "$container_id" ]]; then
        # Exact ID and per-run ownership label, never a name prefix or wildcard.
        if [[ ! "$container_id" =~ ^[0-9a-f]{64}$ ]] ||
            [[ "$(docker inspect --format '{{index .Config.Labels "agently.cleanup-heartbeat-repro"}}' "$container_id")" != "$run_token" ]]; then
            printf 'Could not validate ownership; refusing container removal: %s\n' "$container_name" >&2
            exit 1
        fi
        if ! docker rm --force --volumes "$container_id" >/dev/null; then
            printf 'Could not remove test container %s\n' "$container_name" >&2
            exit 1
        fi
        printf 'Removed disposable test container and its fixture: %s\n' "$container_name"
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Starting %s: mysql:8.0.39 on 127.0.0.1:3308 (existing MySQL on 3307 is untouched).\n' "$container_name"
container_id="$(MYSQL_ROOT_PASSWORD="$repro_password" docker create \
    --name "$container_name" --label "agently.cleanup-heartbeat-repro=$run_token" \
    --publish 127.0.0.1:3308:3306 --tmpfs /var/lib/mysql:rw,size=1g \
    --env MYSQL_ROOT_PASSWORD --env MYSQL_ROOT_HOST=% \
    --env MYSQL_DATABASE=cleanup_heartbeat_repro_local \
    mysql:8.0.39 --performance-schema=ON --default-time-zone=+00:00 \
    --innodb-lock-wait-timeout=3)"
docker start "$container_id" >/dev/null

ready=0
for ((attempt=0; attempt<90; attempt++)); do
    if docker exec --env MYSQL_PWD="$repro_password" "$container_id" \
        mysql --user=root --host=127.0.0.1 --protocol=TCP --execute='SELECT 1' >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done
if [[ "$ready" != 1 ]]; then
    printf 'Test MySQL did not become ready within 90s.\n' >&2
    docker logs --tail 50 "$container_id" >&2
    exit 1
fi
# The flat schema.ddl lacks some current columns (e.g. model_call.run_id).
# Use the full canonical migrations, rewriting the quoted database identifier
# (CREATE/USE/ALTER DATABASE) in the stream, not the source or helper names.
# DROP/migration statements only reach this NEW container.
sed 's/`agently`/`cleanup_heartbeat_repro_local`/g' "$script_dir/schema_versioned.ddl" |
    docker exec --interactive --env MYSQL_PWD="$repro_password" "$container_id" \
        mysql --user=root cleanup_heartbeat_repro_local

export AGENTLY_TEST_CLEANUP_HEARTBEAT_DSN="root:$repro_password@tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local?parseTime=true&loc=UTC"
printf 'Running real-worker regression %s time(s); PASS requires heartbeat renewal during slow DELETE and successful commit.\n' "$repetitions"
"$go_bin" version
cd -- "$core_root"
"$go_bin" test -mod=readonly ./service/maintenance \
    -run '^TestCleanupHeartbeatSelfContentionMySQL$' -count="$repetitions" \
    -timeout="$((repetitions * 60 + 120))s" -v
