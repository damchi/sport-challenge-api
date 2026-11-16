#!/usr/bin/env bash
# What the deployment (deploy.yml) deploys. Run in a clone that has the history
# and the tags, with origin/main fetched. Reads EVENT (workflow_dispatch or
# workflow_run), HEAD_SHA (the commit of the run), TARGET and REF (the
# environment and the tag or commit of a rollback). Prints key=value lines:
#   sha, version, rollback, staging, production
# A merge to main goes to production when its commit carries a version tag
# (the Release workflow gives one to every merge). Staging is only deployed by
# hand (a rollback run).
set -euo pipefail

staging=false
production=false
rollback=false

if [ "$EVENT" = workflow_dispatch ]; then
  rollback=true
  case "$REF" in -*) echo "::error::invalid ref" >&2; exit 1 ;; esac
  sha=$(git rev-parse --verify "$REF^{commit}")
  # Only what was merged into main is ever deployed.
  git merge-base --is-ancestor "$sha" origin/main || { echo "::error::$REF is not in main" >&2; exit 1; }
  if [ "$TARGET" = production ]; then production=true; else staging=true; fi
else
  sha=$HEAD_SHA
  # A version tag on the commit: the release goes on to production.
  if [ -n "$(git tag --points-at "$sha" --list 'v[0-9]*')" ]; then production=true; fi
fi

version=$(git tag --points-at "$sha" --list 'v[0-9]*' | sort -V | tail -n 1)
# Between two releases: the last version, the commits since, the commit.
[ -n "$version" ] || version=$(git describe --tags --match 'v[0-9]*' --always "$sha")
# A repository without any tag: git describe gives the bare commit.
case "$version" in v*) ;; *) version=v0.0.0-$version ;; esac

echo "sha=$sha"
echo "version=$version"
echo "rollback=$rollback"
echo "staging=$staging"
echo "production=$production"
