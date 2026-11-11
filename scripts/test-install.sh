#!/bin/sh
# Test install.sh against a fake release served from a local directory.
#
# Usage: scripts/test-install.sh <dist-dir>
#   <dist-dir> holds this machine's aegis-mcp-<os>-<arch> binary and SHA256SUMS
#   (make dist, or the release workflow's dist/).
# Needs python3 for the local web servers (scripts/test-server.py).
set -eu

dist="$(cd "${1:?usage: test-install.sh <dist-dir>}" && pwd)"
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
pids=""
cleanup() { for p in $pids; do kill "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

# start <name> files|aegis <dir|-> — run scripts/test-server.py and set
# $<name>_url once it has answered a request.
start() {
  python3 "$root/scripts/test-server.py" "$2" "$3" "$work/$1.port" >"$work/$1.log" 2>&1 &
  pids="$pids $!"
  i=0
  until [ -s "$work/$1.port" ]; do
    i=$((i + 1)); [ "$i" -lt 100 ] || fail "$1 server did not start: $(cat "$work/$1.log")"; sleep 0.1
  done
  url="http://127.0.0.1:$(cat "$work/$1.port")"
  # Any HTTP answer (a 401 from the fake Aegis included) means it is serving.
  python3 -c "
import sys, urllib.request, urllib.error
try: urllib.request.urlopen(sys.argv[1], timeout=10)
except urllib.error.HTTPError: pass" "$url/" || fail "$1 server does not answer: $(cat "$work/$1.log")"
  eval "$1_url=\$url"
}

# A web root laid out like GitHub's release download URLs.
mkdir -p "$work/good/latest/download" "$work/good/download/v9.9.9"
cp "$dist"/aegis-mcp-* "$dist/SHA256SUMS" "$work/good/latest/download/"
cp "$dist"/aegis-mcp-* "$dist/SHA256SUMS" "$work/good/download/v9.9.9/"
start good files "$work/good"

# The same release with one checksum changed.
mkdir -p "$work/bad/latest/download"
cp "$dist"/aegis-mcp-* "$work/bad/latest/download/"
sed 's/^\(.\)/0/' "$dist/SHA256SUMS" > "$work/bad/latest/download/SHA256SUMS"
start bad files "$work/bad"

# A fake Aegis that accepts the token "good".
start aegis aegis -

install() { # install <base-url> <dir> [VAR=value…]
  base="$1"; dir="$2"; shift 2
  env -i PATH="/usr/bin:/bin" HOME="$work/home" AEGIS_MCP_RELEASES_URL="$base" \
    AEGIS_MCP_INSTALL_DIR="$dir" AEGIS_MCP_CONFIG="$work/config.json" "$@" \
    sh "$root/install.sh" </dev/null
}

# 1. Latest, install only.
out="$(install "$good_url" "$work/bin1" AEGIS_MCP_NO_SETUP=1)" || fail "install: $out"
"$work/bin1/aegis-mcp" --version >/dev/null || fail "installed binary does not run"
echo "$out" | grep -q "Checksum verified" || fail "no checksum line: $out"
echo "$out" | grep -q "not on your PATH" || fail "no PATH hint: $out"
echo "$out" | grep -q "aegis-mcp setup" || fail "no next step: $out"
pass "installs the latest release and explains the next step"

# 2. A pinned version.
install "$good_url" "$work/bin2" AEGIS_MCP_NO_SETUP=1 AEGIS_MCP_VERSION=v9.9.9 >/dev/null ||
  fail "pinned version"
pass "installs a pinned version"

# 3. A damaged download is refused and nothing is installed.
if out="$(install "$bad_url" "$work/bin3" AEGIS_MCP_NO_SETUP=1 2>&1)"; then fail "bad checksum accepted"; fi
echo "$out" | grep -q "checksum mismatch" || fail "unexpected error: $out"
[ ! -e "$work/bin3/aegis-mcp" ] || fail "binary installed despite a bad checksum"
pass "refuses a download that does not match SHA256SUMS"

# 4. A missing release fails clearly.
if install "$good_url" "$work/bin4" AEGIS_MCP_NO_SETUP=1 AEGIS_MCP_VERSION=v0.0.0 >/dev/null 2>&1; then
  fail "missing release accepted"
fi
pass "fails on a release that does not exist"

# 5. Scripted setup: server and token from the environment.
out="$(install "$good_url" "$work/bin5" AEGIS_SERVER="$aegis_url" AEGIS_TOKEN=good)" ||
  fail "scripted setup: $out
fake Aegis log: $(cat "$work/aegis.log")"
echo "$out" | grep -q "Signed in to $aegis_url as ada" || fail "setup did not sign in: $out"
grep -q '"token": "good"' "$work/config.json" || fail "config file not written"
pass "runs setup without questions when AEGIS_SERVER and AEGIS_TOKEN are set"

# 6. No terminal and nothing set: install, then say what to do.
out="$(install "$good_url" "$work/bin6" PATH="$work/bin6:/usr/bin:/bin")" || fail "no-tty install: $out"
echo "$out" | grep -q "Next: run 'aegis-mcp setup'" || fail "no next step without a terminal: $out"
echo "$out" | grep -q "not on your PATH" && fail "PATH hint shown although it is on PATH"
pass "without a terminal, installs and says to run setup"

echo "install.sh: all tests passed"
