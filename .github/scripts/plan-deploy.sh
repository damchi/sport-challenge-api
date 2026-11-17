#!/usr/bin/env bash
# What the deployment (deploy.yml) deploys. Run in a clone that has the history
# and the tags, with origin/main fetched. Reads EVENT (workflow_dispatch or
# workflow_run), HEAD_SHA (the commit of the run) and REF (the tag or commit
# of a rollback). Prints key=value lines:
#   sha, version, rollback, production
# A merge to main goes to production when its commit carries a version tag
# (the Release workflow gives one to every merge that changes code, none to a
# documentation-only one, release-needed.sh); a rollback always does.
set -euo pipefail

# The version tags of a commit, without the pre-releases (v1.2.3-rc1).
release_tags() { git tag --points-at "$1" --list 'v[0-9]*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true; }

production=false
rollback=false

if [ "$EVENT" = workflow_dispatch ]; then
  rollback=true
  case "$REF" in -*) echo "::error::invalid ref" >&2; exit 1 ;; esac
  sha=$(git rev-parse --verify "$REF^{commit}")
  # Only what was merged into main is ever deployed.
  git merge-base --is-ancestor "$sha" origin/main || { echo "::error::$REF is not in main" >&2; exit 1; }
  production=true
else
  sha=$HEAD_SHA
  # A version tag on the commit: the release goes on to production.
  if [ -n "$(release_tags "$sha")" ]; then production=true; fi
fi

version=$(release_tags "$sha" | sort -V | tail -n 1)
# Between two releases: the last version, the commits since, the commit.
[ -n "$version" ] || version=$(git describe --tags --match 'v[0-9]*' --exclude 'v*-*' --always "$sha")
# A repository without any tag: git describe gives the bare commit.
case "$version" in v*) ;; *) version=v0.0.0-$version ;; esac

echo "sha=$sha"
echo "version=$version"
echo "rollback=$rollback"
echo "production=$production"
