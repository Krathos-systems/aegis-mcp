#!/bin/sh
# Fail if any file given contains a pattern that must never be published.
#
# The patterns themselves are private (internal host names, paths and the like),
# so they are not in this public repository: they come from the LEAK_PATTERNS
# environment variable, one fixed string per line, set from a repository secret.
#
# Usage: LEAK_PATTERNS="..." scripts/leak-check.sh <file>...
set -eu
if [ -z "${LEAK_PATTERNS:-}" ]; then
  echo "leak-check: LEAK_PATTERNS is not set; nothing checked" >&2
  [ "${LEAK_CHECK_REQUIRED:-0}" = "1" ] && exit 1
  exit 0
fi

patterns="$(mktemp)"
trap 'rm -f "$patterns"' EXIT
printf '%s\n' "$LEAK_PATTERNS" | sed '/^[[:space:]]*$/d' > "$patterns"

# -a reads binaries as text; -i because host names are case-insensitive.
# Print only file names, never the matching text: the patterns are secret too.
if hits="$(grep -a -i -l -F -f "$patterns" "$@")"; then
  echo "leak-check: these files contain a private pattern:" >&2
  printf '  %s\n' $hits >&2
  exit 1
fi
echo "leak-check: $# files clean"
