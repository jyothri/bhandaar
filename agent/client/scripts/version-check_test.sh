#!/usr/bin/env bash
# Runs version-check.sh against scratch git repos with tags.
# Usage: agent/client/scripts/version-check_test.sh
set -euo pipefail

script=$(cd "$(dirname "$0")" && pwd)/version-check.sh
failures=0
repos=()
trap 'rm -rf "${repos[@]}"' EXIT

new_repo() {
  repo=$(mktemp -d)
  repos+=("$repo")
  cd "$repo"
  git init -q -b main
  git config user.email test@example.com
  git config user.name test
  set_version "$1"
  mkdir -p agent/wire && echo 'package wire' >agent/wire/doc.go
  echo 'package main' >agent/client/main.go
  commit "initial"
}
set_version() {
  mkdir -p agent/client/internal/version
  printf 'package version\n\nconst Version = "%s"\n' "$1" >agent/client/internal/version/version.go
}
commit() { git add -A && git commit -q -m "$1"; }
touch_file() { mkdir -p "$(dirname "$1")" && echo "// $RANDOM" >>"$1"; }

# expect NAME pass|fail [release]: runs the script with EVENT/BASE_SHA as set.
expect() {
  local name=$1 want=$2 want_release=${3:-} out status
  out=$(GITHUB_OUTPUT='' "$script" 2>/dev/null) && status=pass || status=fail
  if [[ $status != "$want" ]]; then
    echo "FAIL $name: got $status, want $want"; failures=$((failures + 1)); return
  fi
  if [[ -n $want_release && $out != *"release=$want_release"* ]]; then
    echo "FAIL $name: output $out, want release=$want_release"; failures=$((failures + 1)); return
  fi
  echo "ok   $name"
}

# --- no release yet
new_repo 0.1.0
base=$(git rev-parse HEAD)
touch_file agent/client/main.go && commit "change"
EVENT=pull_request BASE_SHA=$base expect "first release, PR" pass false
EVENT=push expect "first release, push" pass true
EVENT=workflow_dispatch expect "first release, manual run" pass true

# --- driveagent/v0.1.0 released at HEAD
git tag driveagent/v0.1.0
EVENT=push expect "re-run after release" pass false
base=$(git rev-parse HEAD)

touch_file agent/client/internal/scan/scan.go && commit "agent change"
EVENT=pull_request BASE_SHA=$base expect "agent change without bump" fail
EVENT=push expect "released version changed on main" fail
set_version 0.1.1 && commit "bump"
EVENT=pull_request BASE_SHA=$base expect "agent change with bump" pass false
EVENT=push expect "bumped on main" pass true

git reset -q --hard "$base"
touch_file agent/wire/changes.go && commit "wire change"
EVENT=pull_request BASE_SHA=$base expect "wire change without bump" fail

git reset -q --hard "$base"
touch_file agent/client/README.md
touch_file agent/client/internal/scan/scan_test.go
touch_file agent/client/internal/identity/testdata/linux/udev.txt
touch_file agent/client/scripts/tool.sh
touch_file agent/wire/wire_test.go
touch_file .github/workflows/driveagent.yml
touch_file be/main.go
commit "no release-relevant changes"
EVENT=pull_request BASE_SHA=$base expect "docs/tests/fixtures/scripts/workflow only" pass false
EVENT=push expect "released, only irrelevant changes since" pass false

git reset -q --hard "$base"
set_version 0.0.9 && touch_file agent/client/main.go && commit "downgrade"
EVENT=pull_request BASE_SHA=$base expect "version below latest" fail
EVENT=push expect "version below latest on main" fail

# --- versions compare numerically, not as strings
new_repo 0.10.0
git tag driveagent/v0.10.0
base=$(git rev-parse HEAD)
set_version 0.9.0 && touch_file agent/client/main.go && commit "0.9.0"
EVENT=pull_request BASE_SHA=$base expect "0.9.0 is below 0.10.0" fail
set_version 0.11.0 && commit "0.11.0"
EVENT=pull_request BASE_SHA=$base expect "0.11.0 is above 0.10.0" pass false

# --- bad input
set_version 1.0 && commit "bad version"
EVENT=push expect "version not MAJOR.MINOR.PATCH" fail
set_version 1.0.0 && commit "good version"
EVENT=pull_request BASE_SHA='' expect "PR without BASE_SHA" fail
EVENT='' expect "no EVENT" fail

if ((failures > 0)); then
  echo "$failures case(s) failed"; exit 1
fi
echo "all cases passed"
