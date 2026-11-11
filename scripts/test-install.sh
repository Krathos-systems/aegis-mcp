#!/bin/sh
# Test install.sh against a fake release served from a local directory.
#
# Usage: scripts/test-install.sh <dist-dir>
#   <dist-dir> holds this machine's aegis-mcp-<os>-<arch> binary and SHA256SUMS
#   (make dist, or the release workflow's dist/).
# Needs python3 for the local web server.
set -eu

dist="$(cd "${1:?usage: test-install.sh <dist-dir>}" && pwd)"
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
pids=""
cleanup() { for p in $pids; do kill "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

# A web root laid out like GitHub's release download URLs.
serve() { # serve <dir> <port>
  python3 -m http.server --bind 127.0.0.1 --directory "$1" "$2" >/dev/null 2>&1 &
  pids="$pids $!"
  i=0
  until python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:$2/')" 2>/dev/null; do
    i=$((i + 1)); [ "$i" -lt 50 ] || fail "web server on $2 did not start"; sleep 0.1
  done
}
mkdir -p "$work/good/latest/download" "$work/good/download/v9.9.9"
cp "$dist"/aegis-mcp-* "$dist/SHA256SUMS" "$work/good/latest/download/"
cp "$dist"/aegis-mcp-* "$dist/SHA256SUMS" "$work/good/download/v9.9.9/"
serve "$work/good" 18741

# The same release with one checksum changed.
mkdir -p "$work/bad/latest/download"
cp "$dist"/aegis-mcp-* "$work/bad/latest/download/"
sed 's/^\(.\)/0/' "$dist/SHA256SUMS" > "$work/bad/latest/download/SHA256SUMS"
serve "$work/bad" 18742

# A fake Aegis that accepts the token "good".
cat > "$work/aegis.py" <<'EOF'
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        ok = self.headers.get("Authorization") == "Bearer good"
        self.send_response(200 if ok else 401)
        self.end_headers()
        self.wfile.write(b'{"name":"ada","level":"editor"}' if ok else b'{"error":"no"}')
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 18743), H).serve_forever()
EOF
python3 "$work/aegis.py" & pids="$pids $!"

install() { # install <base-url> <dir> [VAR=value…]
  base="$1"; dir="$2"; shift 2
  env -i PATH="/usr/bin:/bin" HOME="$work/home" AEGIS_MCP_RELEASES_URL="$base" \
    AEGIS_MCP_INSTALL_DIR="$dir" AEGIS_MCP_CONFIG="$work/config.json" "$@" \
    sh "$root/install.sh" </dev/null
}

# 1. Latest, install only.
out="$(install http://127.0.0.1:18741 "$work/bin1" AEGIS_MCP_NO_SETUP=1)" || fail "install: $out"
"$work/bin1/aegis-mcp" --version >/dev/null || fail "installed binary does not run"
echo "$out" | grep -q "Checksum verified" || fail "no checksum line: $out"
echo "$out" | grep -q "not on your PATH" || fail "no PATH hint: $out"
echo "$out" | grep -q "aegis-mcp setup" || fail "no next step: $out"
pass "installs the latest release and explains the next step"

# 2. A pinned version.
install http://127.0.0.1:18741 "$work/bin2" AEGIS_MCP_NO_SETUP=1 AEGIS_MCP_VERSION=v9.9.9 >/dev/null ||
  fail "pinned version"
pass "installs a pinned version"

# 3. A damaged download is refused and nothing is installed.
if out="$(install http://127.0.0.1:18742 "$work/bin3" AEGIS_MCP_NO_SETUP=1 2>&1)"; then fail "bad checksum accepted"; fi
echo "$out" | grep -q "checksum mismatch" || fail "unexpected error: $out"
[ ! -e "$work/bin3/aegis-mcp" ] || fail "binary installed despite a bad checksum"
pass "refuses a download that does not match SHA256SUMS"

# 4. A missing release fails clearly.
if install http://127.0.0.1:18741 "$work/bin4" AEGIS_MCP_NO_SETUP=1 AEGIS_MCP_VERSION=v0.0.0 >/dev/null 2>&1; then
  fail "missing release accepted"
fi
pass "fails on a release that does not exist"

# 5. Scripted setup: server and token from the environment.
out="$(install http://127.0.0.1:18741 "$work/bin5" AEGIS_SERVER=http://127.0.0.1:18743 AEGIS_TOKEN=good)" ||
  fail "scripted setup: $out"
echo "$out" | grep -q "Signed in to http://127.0.0.1:18743 as ada" || fail "setup did not sign in: $out"
grep -q '"token": "good"' "$work/config.json" || fail "config file not written"
pass "runs setup without questions when AEGIS_SERVER and AEGIS_TOKEN are set"

# 6. No terminal and nothing set: install, then say what to do.
out="$(install http://127.0.0.1:18741 "$work/bin6" PATH="$work/bin6:/usr/bin:/bin")" || fail "no-tty install: $out"
echo "$out" | grep -q "Next: run 'aegis-mcp setup'" || fail "no next step without a terminal: $out"
echo "$out" | grep -q "not on your PATH" && fail "PATH hint shown although it is on PATH"
pass "without a terminal, installs and says to run setup"

echo "install.sh: all tests passed"
