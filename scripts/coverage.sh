#!/bin/sh
# Coverage gate: every function must be 100% covered by the tests.
# main() is covered by TestMainProcess, which runs it in a child copy of the
# test binary; Go merges that child's coverage into the profile.
#
# Usage: scripts/coverage.sh [profile]   (default .gocache/cover.out)
set -eu
profile="${1:-.gocache/cover.out}"
go test -race -covermode=atomic -coverprofile="$profile" ./...
go tool cover -func="$profile" | awk '
  $1 == "total:" { total = $3; next }
  $3 != "100.0%" { print "not fully covered: " $1 " " $2 " " $3; bad = 1 }
  END { print "total coverage: " total; exit bad }'
