#!/usr/bin/env bash

set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
status=0
for migration_dir in "$repo_dir/services/api/recordstore/migrations" "$repo_dir/services/api/languages/migrations"; do
  [[ -d "$migration_dir" ]] || continue
  previous_version=-1
  while IFS= read -r migration; do
    name=${migration##*/}
    valid_name=true
    if [[ "$migration_dir" == */recordstore/migrations ]]; then
      [[ "$name" =~ ^[0-9]{6}_[A-Za-z0-9_-]+\.up\.sql$ ]] || valid_name=false
    else
      [[ "$name" =~ ^[0-9]{3}_[A-Za-z0-9_-]+\.sql$ ]] || valid_name=false
    fi
    if [[ "$valid_name" != true ]]; then
      printf 'unsupported migration filename: %s\n' "$migration" >&2
      status=1
      continue
    fi
    version=${name%%_*}
    version_number=$((10#$version))
    if [[ "$migration_dir" == */recordstore/migrations ]] && (( version_number <= previous_version )); then
      printf 'migration versions are not strictly increasing in %s\n' "$migration_dir" >&2
      status=1
    fi
    previous_version=$version_number
    if grep -Eiq '(^|[^A-Za-z])(BEGIN|COMMIT|ROLLBACK)[[:space:]]*;' "$migration"; then
      printf 'migration must not manage its own transaction: %s\n' "$migration" >&2
      status=1
    fi
  done < <(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print | sort)
done

if (( status != 0 )); then
  exit "$status"
fi
printf 'migration compatibility checks passed (up-only, ordered, transaction-owned by application)\n'
