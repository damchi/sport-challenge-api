#!/usr/bin/env bash
# Tests of next-version.sh: each case gives the current version, the commit
# messages (separated by |, as "subject" or "subject\nBODY") and the version
# expected ("" for no release).
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
failed=0

check() {
  local name=$1 current=$2 messages=$3 want=$4 got
  got=$(printf '%b' "${messages//|/\\x00}\\x00" | "$here/next-version.sh" "$current")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "first release from a feat"       ""        "docs: notes|feat(api): add x"          "v0.1.0"
check "first release from a fix"        ""        "fix: a bug"                            "v0.1.0"
check "no releasable commit, no tag"    ""        "docs: notes|chore: tidy"               ""
check "feat raises the minor"           "v1.2.3"  "feat(activity): add y"                 "v1.3.0"
check "fix raises the patch"            "v1.2.3"  "fix(worker): z"                        "v1.2.4"
check "feat wins over fix"              "v1.2.3"  "fix: a|feat: b|fix: c"                 "v1.3.0"
check "bang is a major"                 "v1.2.3"  "feat(api)!: drop x"                    "v2.0.0"
check "bang without a scope"            "v1.2.3"  "fix!: drop x"                          "v2.0.0"
check "BREAKING CHANGE line is a major" "v1.2.3"  "feat: a\n\nBREAKING CHANGE: b"         "v2.0.0"
check "BREAKING-CHANGE with a dash"     "v1.2.3"  "fix: a\n\nBREAKING-CHANGE: b"          "v2.0.0"
check "major 0: breaking is a minor"    "v0.4.1"  "feat!: drop x"                         "v0.5.0"
check "major 0: feat is a minor"        "v0.4.1"  "feat: x"                               "v0.5.0"
check "major 0: fix is a patch"         "v0.4.1"  "fix: x"                                "v0.4.2"
check "docs, ci, chore: no release"     "v1.2.3"  "docs: a|ci: b|chore: c|test: d|refactor: e" ""
check "merge commits are ignored"       "v1.2.3"  "Merge pull request #1 from x/feat-y"   ""
check "a feat inside a body is ignored" "v1.2.3"  "docs: a\n\nfeat: not a subject"        ""
check "entries as git log writes them"  "v1.2.3"  "\nfeat: a|\nchore: b"                   "v1.3.0"
check "uppercase type is not conventional" "v1.2.3" "Feat: a|FIX: b"                       ""
check "BREAKING CHANGE must start a line"  "v1.2.3" "fix: a\n\nsee BREAKING CHANGE: b"    "v1.2.4"
check "a subject of spaces is no release"  "v1.2.3" "feat:   "                              ""
check "a message starting with a digit"    "v1.2.3" "1 feat: a|fix: b"                      "v1.2.4"
check "no commit at all"                "v1.2.3"  ""                                      ""
check "double-digit parts"              "v1.9.19" "fix: x"                                "v1.9.20"
check "minor resets the patch"          "v1.2.9"  "feat: x"                               "v1.3.0"
check "major resets the others"         "v1.2.9"  "feat!: x"                              "v2.0.0"

# A long body: grep must not be cut short by the end of a pipe (SIGPIPE).
big=$(head -c 400000 /dev/zero | tr '\0' 'x')
got=$(printf 'fix: a\n\nBREAKING CHANGE: b\n%s\n\0' "$big" | "$here/next-version.sh" "v1.0.0")
if [[ "$got" == "v2.0.0" ]]; then
  echo "ok   BREAKING CHANGE in a very long message"
else
  echo "FAIL BREAKING CHANGE in a very long message: got '$got', want 'v2.0.0'"
  failed=1
fi

# The last entry, even without its NUL.
got=$(printf 'feat: a' | "$here/next-version.sh" "v1.0.0")
if [[ "$got" == "v1.1.0" ]]; then
  echo "ok   an entry without its NUL"
else
  echo "FAIL an entry without its NUL: got '$got', want 'v1.1.0'"
  failed=1
fi

if got=$(printf 'fix: x\0' | "$here/next-version.sh" "1.2.3" 2>&1); then
  echo "FAIL a version without v is refused"
  failed=1
else
  echo "ok   a version without v is refused"
fi

exit "$failed"
