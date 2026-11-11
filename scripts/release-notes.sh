#!/bin/sh
# Print the CHANGELOG.md section for one tag, without its heading.
# Fails if the section is missing or empty, so a release cannot ship without notes.
#
# Usage: scripts/release-notes.sh v0.1.0-alpha.1 [CHANGELOG.md]
set -eu
tag="${1:?usage: release-notes.sh <tag> [changelog]}"
changelog="${2:-CHANGELOG.md}"

notes="$(awk -v tag="$tag" '
  /^## / {
    if (found) exit
    # The heading is "## <tag>" optionally followed by " — <date>".
    split($0, words, " ")
    if (words[2] == tag) { found = 1; next }
  }
  found { print }
' "$changelog")"

# Trim blank lines at both ends.
notes="$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"

if [ -z "$notes" ]; then
  echo "release-notes: no '## $tag' section in $changelog (or it is empty)" >&2
  exit 1
fi
printf '%s\n' "$notes"
