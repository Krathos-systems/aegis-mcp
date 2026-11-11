#!/bin/sh
# Install aegis-mcp on macOS or Linux, then run `aegis-mcp setup`.
#
#   curl -fsSL https://github.com/Krathos-systems/aegis-mcp/releases/latest/download/install.sh | sh
#
# Options, as environment variables:
#   AEGIS_MCP_VERSION      a release tag to install instead of the latest (e.g. v0.1.0)
#   AEGIS_MCP_INSTALL_DIR  where to put the binary (default ~/.local/bin)
#   AEGIS_MCP_NO_SETUP=1   install only; run `aegis-mcp setup` yourself later
#   AEGIS_SERVER, AEGIS_TOKEN
#                          set both to run setup without questions (for scripts)
#
# Needs curl or wget, and sha256sum or shasum. Installs for the current user
# only; never needs sudo.
set -eu

repo_url="${AEGIS_MCP_RELEASES_URL:-https://github.com/Krathos-systems/aegis-mcp/releases}"
version="${AEGIS_MCP_VERSION:-latest}"
install_dir="${AEGIS_MCP_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'aegis-mcp install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "this installer is for macOS and Linux; on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "no build for $(uname -m)" ;;
esac
asset="aegis-mcp-$os-$arch"

if [ "$version" = latest ]; then
  base="$repo_url/latest/download"
else
  base="$repo_url/download/$version"
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https,http' -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail "needs curl or wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "needs sha256sum or shasum to check the download"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $asset ($version)…"
fetch "$base/$asset" "$tmp/$asset" || fail "download failed: $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "download failed: $base/SHA256SUMS"

want="$(awk -v f="$asset" '$2 == f || $2 == "*"f { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$want" ] || fail "SHA256SUMS has no entry for $asset"
got="$(sha256 "$tmp/$asset")"
[ "$got" = "$want" ] || fail "checksum mismatch for $asset: the download is damaged or was altered"
say "✓ Checksum verified"

mkdir -p "$install_dir"
dest="$install_dir/aegis-mcp"
chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$dest"
say "✓ Installed $("$dest" --version) to $dest"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) say "! $install_dir is not on your PATH. Add it to your shell profile:"
     say "    export PATH=\"$install_dir:\$PATH\"" ;;
esac

if [ "${AEGIS_MCP_NO_SETUP:-}" = 1 ]; then
  say "Next: run 'aegis-mcp setup' to connect it to Aegis."
elif [ -n "${AEGIS_SERVER:-}" ] && [ -n "${AEGIS_TOKEN:-}" ]; then
  say ""
  printf '%s\n' "$AEGIS_TOKEN" | AEGIS_TOKEN= "$dest" setup --server "$AEGIS_SERVER" --token-stdin
elif (: </dev/tty) 2>/dev/null; then
  say ""
  # The script itself arrives on stdin through the pipe, so answers come
  # from the terminal.
  "$dest" setup </dev/tty
else
  say "Next: run 'aegis-mcp setup' to connect it to Aegis."
fi
