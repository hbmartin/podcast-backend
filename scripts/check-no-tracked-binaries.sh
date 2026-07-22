#!/usr/bin/env bash
set -euo pipefail

max_bytes=$((10 * 1024 * 1024))
failed=0

while IFS= read -r -d '' path; do
  [[ -f "$path" ]] || continue
  size=$(wc -c < "$path")
  kind=$(file -b "$path")
  if (( size > max_bytes )) || [[ "$kind" == ELF* ]] || [[ "$kind" == Mach-O* ]] || [[ "$kind" == PE32* ]]; then
    echo "tracked binary or oversized artifact: $path ($size bytes; $kind)" >&2
    failed=1
  fi
done < <(git ls-files -z)

exit "$failed"
