#!/usr/bin/env bash
set -euo pipefail

repo_path="${1:-}"
workspace_path="${2:-}"
testdata_path="${3:-}"

if [[ -z "$repo_path" || -z "$workspace_path" || -z "$testdata_path" ]]; then
  echo "usage: $0 <repo_path> <workspace_path> <testdata_path>" >&2
  exit 1
fi

for dir in "$repo_path" "$testdata_path" "$testdata_path/agents" "$testdata_path/mcp" "$testdata_path/models" "$testdata_path/tools/bundles"; do
  if [[ ! -d "$dir" ]]; then
    echo "missing required directory: $dir" >&2
    exit 1
  fi
done

# By default create an isolated workspace per prep call and expose it under
# the stable workspace_path via symlink (run-scoped isolation).
randomize_workspace="${AGENTLY_E2E_WORKSPACE_RANDOM:-1}"
effective_workspace_path="$workspace_path"
if [[ "$randomize_workspace" == "1" ]]; then
  parent_dir="$(dirname "$workspace_path")"
  mkdir -p "$parent_dir"
  run_root="$(mktemp -d "$parent_dir/run-XXXXXX")"
  effective_workspace_path="$run_root/.agently"
  mkdir -p "$effective_workspace_path"
fi

mkdir -p "$effective_workspace_path"
mkdir -p \
  "$effective_workspace_path/agents" \
  "$effective_workspace_path/mcp" \
  "$effective_workspace_path/models" \
  "$effective_workspace_path/tools/bundles" \
  "$effective_workspace_path/tools/instructions" \
  "$effective_workspace_path/oauth" \
  "$effective_workspace_path/a2a" \
  "$effective_workspace_path/workflows" \
  "$effective_workspace_path/feeds" \
  "$effective_workspace_path/embedders" \
  "$effective_workspace_path/state"

rsync -a --delete "$testdata_path/agents/" "$effective_workspace_path/agents/"
rsync -a --delete "$testdata_path/mcp/" "$effective_workspace_path/mcp/"
rsync -a --delete "$testdata_path/models/" "$effective_workspace_path/models/"
rsync -a --delete "$testdata_path/tools/bundles/" "$effective_workspace_path/tools/bundles/"

config_file="$effective_workspace_path/config.yaml"
if [[ ! -f "$config_file" ]]; then
  cat > "$config_file" <<'YAML'
models: []
agents: []

internalMCP:
  services:
    - system/exec
    - system/os
YAML
fi

if [[ "$effective_workspace_path" != "$workspace_path" ]]; then
  rm -rf "$workspace_path"
  ln -s "$effective_workspace_path" "$workspace_path"
fi

echo "workspace prepared: $workspace_path"
if [[ "$effective_workspace_path" != "$workspace_path" ]]; then
  echo "workspace target: $effective_workspace_path"
fi
