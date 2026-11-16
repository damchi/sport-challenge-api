#!/usr/bin/env bash
# What the deployment (deploy.yml) deploys. Run in a clone that has the history
# and the tags, with origin/main and origin/staging fetched. Reads EVENT
# (workflow_dispatch or workflow_run), WORKFLOW and BRANCH (the workflow and the
# branch of the run that finished), HEAD_SHA (its commit), TARGET and REF (the
# environment and the tag or commit of a rollback). Prints key=value lines:
#   sha, version, rollback, staging, production
# The branch decides: a green CI on staging goes to staging (when the commit is
# still the tip); a finished Release on main goes to production when the commit
# carries a version tag. Anything else deploys nothing.
set -euo pipefail

staging=false
production=false
rollback=false

if [ "$EVENT" = workflow_dispatch ]; then
  rollback=true
  case "$REF" in -*) echo "::error::invalid ref" >&2; exit 1 ;; esac
  sha=$(git rev-parse --verify "$REF^{commit}")
  # Only what was merged into main (or, for staging, into staging) is ever deployed.
  if [ "$TARGET" = production ]; then
    git merge-base --is-ancestor "$sha" origin/main || { echo "::error::$REF is not in main" >&2; exit 1; }
    production=true
  else
    git merge-base --is-ancestor "$sha" origin/main ||
      git merge-base --is-ancestor "$sha" origin/staging ||
      { echo "::error::$REF is not in main or staging" >&2; exit 1; }
    staging=true
  fi
else
  sha=$HEAD_SHA
  if [ "$WORKFLOW" = CI ] && [ "$BRANCH" = staging ]; then
    # Runs can arrive late: only the tip of staging is deployed.
    if [ "$sha" = "$(git rev-parse origin/staging)" ]; then staging=true; fi
  elif [ "$WORKFLOW" = Release ] && [ "$BRANCH" = main ]; then
    # A version tag on the commit: the release goes to production.
    if [ -n "$(git tag --points-at "$sha" --list 'v[0-9]*')" ]; then production=true; fi
  fi
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
