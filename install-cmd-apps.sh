#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

apps=()
for dir in ./cmd/*; do
  if [[ -d "$dir" && -f "$dir/main.go" ]]; then
    apps+=("$dir")
  fi
done

if [[ ${#apps[@]} -eq 0 ]]; then
  echo "No cmd apps with main.go found under ./cmd"
  exit 1
fi

echo "Installing cmd apps: ${apps[*]}"
go install "${apps[@]}"
echo "Install complete."
