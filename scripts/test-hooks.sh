#!/usr/bin/env bash
# Tests the git hooks in .githooks against a throwaway repository in a temp dir. It never touches this repo.
# Checks: a staged broken file is rejected even when the working tree has a fix; pushing a branch that is not
# checked out tests that branch's tree.
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

fail() { echo "test-hooks: FAIL: $*" >&2; exit 1; }
pass() { echo "test-hooks: ok: $*"; }

repo="$tmp/repo"
git init -q -b main "$repo"
cd "$repo"
git config core.hooksPath .githooks
mkdir -p .githooks scripts a cmd/demo
cp -a "$here/.githooks/." .githooks/
cp -a "$here/scripts/check-colors.sh" scripts/
printf 'module example.com/hooks\n\ngo 1.21\n' > go.mod
printf 'package a\n\nfunc A() int { return 1 }\n' > a/a.go
printf 'package main\n\nimport "example.com/hooks/a"\n\nfunc main() { a.A() }\n' > cmd/demo/main.go
printf 'package a\n\nimport "testing"\n\nfunc TestA(t *testing.T) {\n\tif A() < 0 {\n\t\tt.Fatal("A")\n\t}\n}\n' > a/a_test.go
git add -A
git commit -qm init
git remote add origin "$tmp/remote.git" 2>/dev/null || true
git init -q --bare "$tmp/remote.git"
git push -q origin main

broken='package a

func A() int { return "broken" }
'
fixed='package a

func A() int { return 2 }
'

# 1. Partial staging: the staged version is broken, the working tree has a fix that is not staged.
printf '%s' "$broken" > a/a.go
git add a/a.go
printf '%s' "$fixed" > a/a.go
head_before=$(git rev-parse HEAD)
if git commit -qm "partial"; then fail "a staged broken file was committed with an unstaged fix"; fi
[[ $(git rev-parse HEAD) == "$head_before" ]] || fail "HEAD moved after a rejected commit"
pass "partial staging: the staged broken version is rejected"
git add a/a.go
git commit -qm "fixed" || fail "the staged fix was rejected"
pass "partial staging: the staged fix is accepted"

# 2. Pushing a branch that is not checked out tests that branch's tree. The bad commit is made in this throwaway
# repository without hooks, so that the fixture can exist at all.
git checkout -q -b bad
printf '%s' "$broken" > a/a.go
git add a/a.go
git -c core.hooksPath=/dev/null commit -qm "bad"
git checkout -q main
if git push -q origin bad 2>/dev/null; then fail "pushing a broken branch that is not checked out succeeded"; fi
pass "push of a non-checked-out broken branch is rejected while main is checked out"

git checkout -q -b good
printf 'package a\n\nfunc A() int { return 1 }\n\nfunc B() {}\n' > a/a.go
git add a/a.go
git commit -qm "good"
git checkout -q main
git push -q origin good || fail "pushing a good branch that is not checked out was rejected"
pass "push of a non-checked-out good branch is accepted"
