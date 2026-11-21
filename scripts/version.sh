#!/usr/bin/env bash
# version.sh — version information for the company versioning model (global CLAUDE.md §4).
# Reads git and VERSION only; never writes anything, never uses the network.
#
#   version.sh label            build label for the current checkout
#                               feature/login/x -> 1.5.0-alpha.login.7
#   version.sh status           key=value facts: branch, target, channel, frozen, behind, note
#   version.sh note-path        documents/unreleased/<branch>.md for the current branch
#   version.sh notes            unreleased change notes: "<type>\t<file>"
#   version.sh propose beta     next beta (from change notes, or beta.N+1)
#   version.sh propose release  release number for the current beta
#   version.sh propose next     next alpha target after a release
#
# Run it from anywhere inside the repo, or pass -C <dir> first.
set -u

if [ "${1:-}" = "-C" ]; then cd "$2" || exit 1; shift 2; fi
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "version.sh: not inside a git repo" >&2; exit 1; }
cd "$root" || exit 1

die() { echo "version.sh: $*" >&2; exit 1; }
trim() { tr -d ' \t\r\n'; }

branch=$(git symbolic-ref --short -q HEAD 2>/dev/null || echo "")

# dev_ref — the best local view of develop (remote first: it's what PRs merge into).
dev_ref=""
for r in origin/develop develop; do
  git rev-parse -q --verify "$r^{commit}" >/dev/null 2>&1 && { dev_ref=$r; break; }
done

# version_at REF — VERSION file content at a ref ("" if missing).
version_at() { git show "$1:VERSION" 2>/dev/null | head -n1 | trim; }
worktree_version() { [ -f VERSION ] && head -n1 VERSION | trim; }

base_of() { printf '%s' "${1%%[-+]*}"; }
channel_of() {
  case "$1" in
    *-alpha*) echo alpha ;;
    *-beta*) echo beta ;;
    "") echo none ;;
    *) echo release ;;
  esac
}
valid_semver() { [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; }

# Target version: develop's VERSION is the repo's truth; fall back to this checkout's.
dev_version=""; [ -n "$dev_ref" ] && dev_version=$(version_at "$dev_ref")
cur_version=$(worktree_version)
target_version=${dev_version:-$cur_version}

note_slug() { printf '%s' "${1//\//-}" | tr -c 'A-Za-z0-9._-' '-'; }
note_path() { printf 'documents/unreleased/%s.md' "$(note_slug "$branch")"; }

# sanitize_id S — a valid SemVer pre-release identifier part.
sanitize_id() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed -E 's/-+/-/g; s/^-//; s/-$//'; }

# last_release — highest vX.Y.Z tag without a pre-release part ("0.0.0" if none).
last_release() {
  local t
  t=$(git tag -l 'v[0-9]*' 2>/dev/null | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sed 's/^v//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n1)
  echo "${t:-0.0.0}"
}

# notes — print "<type>\t<file>" for every unreleased change note.
list_notes() {
  local f t
  for f in documents/unreleased/*.md; do
    [ -f "$f" ] || continue
    [ "${f##*/}" = README.md ] && continue
    t=$(sed -n '1,/^---$/{/^type:/{s/^type:[[:space:]]*//;s/[[:space:]#].*//;p;q}}' "$f" 2>/dev/null)
    printf '%s\t%s\n' "${t:-unknown}" "$f"
  done
}

# bump_from_notes BASE — apply the highest change type in the notes to BASE.
bump_from_notes() {
  local base=$1 types major minor patch
  types=$(list_notes | cut -f1)
  IFS=. read -r major minor patch <<<"$base"
  if grep -qx breaking <<<"$types"; then
    if [ "$major" -eq 0 ]; then echo "0.$((minor + 1)).0"; else echo "$((major + 1)).0.0"; fi
  elif grep -qxE 'feature|improvement' <<<"$types"; then
    echo "$major.$((minor + 1)).0"
  elif [ -n "$types" ]; then
    echo "$major.$minor.$((patch + 1))"
  else
    echo ""
  fi
}

# ver_gt A B — true when A > B (X.Y.Z only).
ver_gt() {
  [ "$1" = "$2" ] && return 1
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n1)" = "$1" ]
}

cmd_label() {
  local base n area exact
  base=$(base_of "$target_version")
  [ -n "$base" ] || die "no VERSION file on develop or in this checkout"
  exact=$(git describe --tags --exact-match HEAD 2>/dev/null || true)
  case "$branch" in
    main|master)
      [ -n "$exact" ] && { echo "${exact#v}"; return; }
      echo "$(worktree_version)+$(git rev-parse --short HEAD)" ;;
    develop)
      [ -n "$exact" ] && { echo "${exact#v}"; return; }
      local tag; tag=$(git describe --tags --abbrev=0 2>/dev/null || true)
      if [ -n "$tag" ]; then n=$(git rev-list --count "$tag"..HEAD); else n=$(git rev-list --count HEAD); fi
      echo "${cur_version}.${n}" ;;
    promote/*|hotfix/*)
      echo "${cur_version}+$(git rev-parse --short HEAD)" ;;
    "")
      echo "${cur_version:-0.0.0}+$(git rev-parse --short HEAD)" ;;
    *)
      # feature/<area>/<task>, fix/<area>, anything else: target-alpha.<area>.<commits>
      area=$(printf '%s' "$branch" | cut -d/ -f2)
      [ -n "$area" ] && [ "$area" != "$branch" ] || area=$branch
      area=$(sanitize_id "$area"); [ -n "$area" ] || area=branch
      if [ -n "$dev_ref" ]; then n=$(git rev-list --count "$dev_ref"..HEAD); else n=$(git rev-list --count HEAD); fi
      echo "${base}-alpha.${area}.${n}" ;;
  esac
}

cmd_status() {
  local behind=unknown np exists=no frozen=no
  [ -n "$dev_ref" ] && behind=$(git rev-list --count HEAD.."$dev_ref" 2>/dev/null || echo unknown)
  np=$(note_path)
  [ -f "$np" ] && exists=yes
  [ "$(channel_of "$target_version")" = beta ] && frozen=yes
  printf 'branch=%s\ndevelop_ref=%s\ntarget=%s\nchannel=%s\nfrozen=%s\nbehind_develop=%s\nnote=%s\nnote_exists=%s\nlast_release=%s\nlabel=%s\n' \
    "${branch:-detached}" "${dev_ref:-none}" "${target_version:-none}" "$(channel_of "$target_version")" \
    "$frozen" "$behind" "$np" "$exists" "$(last_release)" "$(cmd_label 2>/dev/null || echo none)"
}

cmd_propose() {
  local what=${1:-} v base ch rel computed n
  v=$target_version; base=$(base_of "$v"); ch=$(channel_of "$v")
  case "$what" in
    beta)
      if [ "$ch" = beta ]; then
        n=${v##*-beta.}; [[ $n =~ ^[0-9]+$ ]] || n=0
        echo "$base-beta.$((n + 1))"; return
      fi
      [ "$ch" = alpha ] || die "develop is at '$v' (not alpha); run 'propose next' first"
      rel=$(last_release)
      computed=$(bump_from_notes "$rel")
      # The notes decide when the target is just the default "next minor" opened after the
      # last release (fixes only -> patch). A target someone opened on purpose (e.g. 2.0.0)
      # wins unless the notes need a bigger step (e.g. a breaking change).
      local dflt=""
      if valid_semver "$rel"; then IFS=. read -r a b _ <<<"$rel"; dflt="$a.$((b + 1)).0"; fi
      if [ -n "$computed" ] && { [ "$base" = "$dflt" ] || ver_gt "$computed" "$base"; }; then base=$computed; fi
      echo "$base-beta.1" ;;
    release)
      [ "$ch" = beta ] || die "develop is at '$v'; a release comes from a beta"
      echo "$base" ;;
    next)
      [ "$ch" = release ] || die "'$v' is not released yet"
      valid_semver "$base" || die "bad version '$base'"
      IFS=. read -r a b _ <<<"$base"
      echo "$a.$((b + 1)).0-alpha" ;;
    *) die "propose beta|release|next" ;;
  esac
}

case "${1:-label}" in
  label) cmd_label ;;
  status) cmd_status ;;
  note-path) [ -n "$branch" ] || die "detached HEAD"; note_path; echo ;;
  notes) list_notes ;;
  propose) cmd_propose "${2:-}" ;;
  -h|--help|help) sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) die "unknown command '$1' (try: label, status, note-path, notes, propose)" ;;
esac
