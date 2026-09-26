#!/usr/bin/env bash
# Decides whether a driveagent change needs a version bump, and whether this
# commit should be released (docs/specs/remote-sync-ci.md, "version-check").
#
# Env:
#   EVENT     pull_request, push or workflow_dispatch
#   BASE_SHA  the pull request's base commit (pull requests only)
# Writes version=<Version> and release=true|false to $GITHUB_OUTPUT, or to
# stdout when that's unset, so it can be run locally too.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail() { echo "version-check: $*" >&2; exit 1; }

version_file=agent/client/internal/version/version.go
[[ -f $version_file ]] || fail "$version_file not found"
v=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$version_file")
[[ $v =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
  fail "Version \"$v\" in $version_file is not MAJOR.MINOR.PATCH"

latest_tag=$(git tag --list 'driveagent/v*' --sort=-v:refname | head -n1)
latest=${latest_tag#driveagent/v}

# Release-relevant files: agent/client and agent/wire, except Markdown, tests,
# test fixtures and agent/client/scripts.
relevant() {
  grep -E '^agent/(client|wire)/' | grep -vE '\.md$|_test\.go$|(^|/)testdata/|^agent/client/scripts/' || true
}
changed_since() { git diff --name-only "$1" HEAD | relevant; }

# gt A B: is version A above version B?
gt() { [[ $1 != "$2" && $(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1) == "$1" ]]; }

show() { printf '  %s\n' "${1//$'\n'/$'\n'  }" >&2; }

release=false
case "${EVENT:-}" in
  pull_request)
    [[ -n ${BASE_SHA:-} ]] || fail "BASE_SHA is required for pull requests"
    files=$(changed_since "$BASE_SHA")
    if [[ -z $files ]]; then
      echo "version-check: no release-relevant changes; no bump needed" >&2
    elif [[ -z $latest ]]; then
      echo "version-check: no driveagent release yet; $v will be the first" >&2
    elif ! gt "$v" "$latest"; then
      echo "version-check: release-relevant changes:" >&2; show "$files"
      fail "agent changed but internal/version.Version ($v) is not above the latest release $latest_tag — bump it"
    else
      echo "version-check: $v is above $latest_tag; it will be released on merge" >&2
    fi
    ;;
  push|workflow_dispatch)
    if ! git rev-parse -q --verify "refs/tags/driveagent/v$v" >/dev/null; then
      if [[ -n $latest ]] && ! gt "$v" "$latest"; then
        fail "Version $v is below the latest release $latest_tag"
      fi
      release=true
      echo "version-check: releasing driveagent/v$v" >&2
    else
      files=$(changed_since "driveagent/v$v")
      if [[ -n $files ]]; then
        echo "version-check: changed since driveagent/v$v:" >&2; show "$files"
        fail "driveagent/v$v is already released, but the agent changed since; bump internal/version.Version in a follow-up PR"
      fi
      echo "version-check: driveagent/v$v is released and nothing relevant changed since" >&2
    fi
    ;;
  *)
    fail "EVENT must be pull_request, push or workflow_dispatch, got \"${EVENT:-}\""
    ;;
esac

out=${GITHUB_OUTPUT:-/dev/stdout}
echo "version=$v" >>"$out"
echo "release=$release" >>"$out"
