#!/bin/sh
set -e

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
OUT="${OUT:-dist}"
CAND="${CAND:-$OUT/candidate}"
GO="${GO:-${AII_GO:-go}}"
phase="${1:-prepare}"
TOOL="$OUT/aii-release"

die() { echo "release: $*" >&2; exit 1; }

authored=$(cat "$root/VERSION" 2>/dev/null) && [ -n "$authored" ] || die "no VERSION file at $root/VERSION"
VERSION="${VERSION:-$authored}"
[ "$VERSION" = "$authored" ] || die "VERSION=$VERSION is set, but the VERSION file says $authored — the binaries report the file's version (it is compiled in); unset VERSION or bump the file"

safe_root() {
  case "$2" in
    ''|/*|.|..|../*|*/..|*/../*) die "$1 must be a relative path under the repo, got '$2'" ;;
  esac
}
safe_root OUT "$OUT"; safe_root CAND "$CAND"

build_tool() { "$GO" build -o "$TOOL" ./cmd/aii-release; }

require_clean_tree() {
  [ -z "$(git status --porcelain)" ] || die "working tree is dirty — a candidate must name a commit that exists"
}

installers() { printf '%s\n' "aii-setup-${VERSION}-amd64.exe" "AII-OS-${VERSION}.dmg"; }

targets() { "$TOOL" assets -version "$VERSION"; }
asset_names() { targets | awk '{print $1}'; }

public_files() {
  for asset in $(asset_names); do printf '%s\n%s\n' "$asset" "$asset.platform.sig"; done
  installers
}

check_candidate() {
  [ -f "$CAND/CANDIDATE.txt" ] || die "candidate carries no CANDIDATE.txt"
  rev=$(awk '$1=="source_rev"{print $2}' "$CAND/CANDIDATE.txt")
  [ -n "$rev" ] || die "candidate carries no source_rev"
  ver=$(awk '$1=="version"{print $2}' "$CAND/CANDIDATE.txt")
  [ "$ver" = "$VERSION" ] || die "candidate is version '$ver'; this tree releases $VERSION"
  want=$(targets | awk '{print $1, $2, $3}' | sort)
  [ -n "$want" ] || die "the release contract names no targets"
  listed=$(awk '$1!="source_rev" && $1!="version"' "$CAND/CANDIDATE.txt" | sort)
  [ "$listed" = "$want" ] || die "CANDIDATE.txt does not list exactly the release's targets, once each.
  expected:
$(printf '%s\n' "$want" | sed 's/^/    /')
  listed:
$(printf '%s\n' "$listed" | sed 's/^/    /')"
  for f in $(asset_names) $(installers); do
    [ -f "$CAND/$f" ] || die "$f is missing from the candidate"
  done
  public=$(public_files)
  extra=$(ls -A "$CAND" | while read -r f; do
    case "$f" in (CANDIDATE.txt|SHA256SUMS|*.payload.json) continue ;; esac
    printf '%s\n' "$public" | grep -qxF "$f" || echo "$f"
  done)
  [ -z "$extra" ] || die "the candidate holds files that are not part of it (remove them; nothing outside the release is ever staged):
$(printf '%s\n' "$extra" | sed 's/^/    /')"
  [ -n "${EVIDENCE:-}" ] || die "set EVIDENCE=<the candidate's signed evidence tarball>; it stays outside the candidate"
  [ -f "$EVIDENCE" ] && [ -f "$EVIDENCE.platform.sig" ] || die "no evidence tarball at '$EVIDENCE' with its .platform.sig beside it"
  out=$("$TOOL" verify -kind evidence -artifact "$EVIDENCE" -sig "$EVIDENCE.platform.sig" \
    -version "$VERSION" -platform evidence -arch bundle -source-rev "$rev" \
    ${ROOT:+-root "$ROOT"} ${TRUST_DIR:+-trust-dir "$TRUST_DIR"} 2>&1) || die "the evidence REFUSED for $rev: $out"
  printf '%s\n' "$out" | sed 's/^/    /'
}

case "$phase" in
prepare)
  require_clean_tree
  rev=$(git rev-parse HEAD)
  echo "=== candidate $VERSION @ $(git rev-parse --short=12 HEAD)"
  rm -rf "$CAND" "$OUT"
  mkdir -p "$CAND" "$OUT"
  build_tool

  echo "=== installers and update archives (one build of each program)"
  BUILD_MACOS=1 OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/build-all.sh || \
    die "packaging failed — a candidate is complete or it is not a candidate"
  for a in "$OUT"/aii-os_*.tar.gz "$OUT"/aii-os_*.zip "$OUT"/*.deb "$OUT"/*.dmg "$OUT"/*.exe "$OUT"/aii-os-app_*.zip; do
    [ -e "$a" ] || continue
    cp "$a" "$CAND/" || die "copy $(basename "$a") into the candidate"
    echo "    $(basename "$a")"
  done

  echo "=== inventory: every supported target must be present"
  missing=0
  for asset in $(asset_names); do
    [ -f "$CAND/$asset" ] || { echo "    MISSING $asset"; missing=$((missing+1)); }
  done
  for inst in $(installers); do
    [ -f "$CAND/$inst" ] || { echo "    MISSING installer $inst"; missing=$((missing+1)); }
  done
  [ "$missing" -eq 0 ] || die "$missing required asset(s) missing"

  echo "=== bind every shipped executable to $rev"
  bind() {
    out=$(GO="$GO" sh packaging/assert-source-bound.sh "$1" "$rev" 2>&1) || die "$out"
    printf '%s\n' "$out" | sed 's/^/    /'
  }
  work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
  for deb in "$CAND"/*.deb; do
    rm -rf "$work/x"; mkdir -p "$work/x"; dpkg-deb -x "$deb" "$work/x"
    bind "$work/x/usr/bin/aii"
    bind "$work/x/usr/bin/aii-app"
    bind "$work/x/usr/bin/aii-vulkan-probe"
  done
  for tgz in "$CAND"/*.tar.gz; do
    rm -rf "$work/x"; mkdir -p "$work/x"; tar -xzf "$tgz" -C "$work/x"
    bind "$work/x/aii"
    bind "$work/x/aii-app"
    case "$tgz" in *_linux_*) bind "$work/x/aii-vulkan-probe" ;; esac
  done
  for z in "$CAND"/aii-os_*_windows_*.zip; do
    rm -rf "$work/x"; mkdir -p "$work/x"; unzip -q "$z" -d "$work/x"
    bind "$work/x/aii.exe"
    bind "$work/x/AII OS.exe"
  done
  for exe in "$CAND"/*.exe; do
    bind "$exe"
  done
  for appz in "$CAND"/aii-os-app_*.zip; do
    rm -rf "$work/x"; mkdir -p "$work/x"; unzip -q "$appz" -d "$work/x"
    bind "$work/x/AII OS.app/Contents/MacOS/aii"
    bind "$work/x/AII OS.app/Contents/MacOS/AII OS"
  done

  echo "=== every archive holds what its platform ships, as an update would install it"
  unsigned=""
  if [ -z "${SIGN_CMD:-}${SIGNTOOL:-}" ]; then
    unsigned="-unsigned windows"
    echo "    NOTE: no SIGN_CMD or SIGNTOOL — the Windows executables are unsigned, and are not asked to be"
  fi
  out=$("$TOOL" complete -dir "$CAND" -version "$VERSION" $unsigned 2>&1) || die "the candidate's archives are not complete:
$out"
  printf '%s\n' "$out" | sed 's/^/    /'

  echo "=== emit signing payloads (rendered by aii-release, never by this script)"
  : > "$CAND/CANDIDATE.txt"
  echo "source_rev $rev" >> "$CAND/CANDIDATE.txt"
  echo "version $VERSION" >> "$CAND/CANDIDATE.txt"
  targets | while read -r asset p a goos; do
    [ -n "$p" ] && [ -n "$a" ] || die "no payload rule for asset $asset (platform '$p', arch '$a')"
    "$TOOL" payload -artifact "$CAND/$asset" -version "$VERSION" \
      -platform "$p" -arch "$a" -source-rev "$rev" > "$CAND/$asset.payload.json"
    echo "$asset $p $a" >> "$CAND/CANDIDATE.txt"
    echo "    $asset.payload.json"
  done

  cat <<MSG

=== candidate prepared: $CAND
Nothing is signed and nothing is public.

At the signing authority (keys never come here), for each payload:
  ai3-bundle create --artifact-kind release.platform_release \\
    --profile AIII-PQ-SIGNATURE-V1-ROOT \\
    --payload <asset>.payload.json --priv <ml> --priv <slh>

The candidate's evidence bundle is signed the same way under its own
kind, so it can never pass as an update:
  aii-release payload -artifact evidence.tar.gz -version $VERSION \\
    -platform evidence -arch bundle -source-rev $rev > evidence.payload.json
  ai3-bundle create --artifact-kind release.evidence_bundle \\
    --profile AIII-PQ-SIGNATURE-V1-ROOT \\
    --payload evidence.payload.json --priv <ml> --priv <slh>
Verify with: aii-release verify -kind evidence -artifact evidence.tar.gz ...

Return each envelope as $CAND/<asset>.platform.sig, keep the signed
evidence tarball outside the candidate with its .platform.sig beside it,
then:
  EVIDENCE=<evidence tarball> sh packaging/release.sh attach
MSG
  ;;

attach)
  [ -d "$CAND" ] || die "no candidate at $CAND — run prepare first"
  build_tool
  echo "=== the candidate is the release's closed set, and its evidence verifies"
  check_candidate
  echo "=== VERIFY every asset the way the updater will"
  n=0
  while read -r asset p a; do
    [ "$asset" = "source_rev" ] || [ "$asset" = "version" ] && continue
    sig="$CAND/$asset.platform.sig"
    [ -f "$sig" ] || die "$asset has no signature"
    out=$("$TOOL" verify -artifact "$CAND/$asset" -sig "$sig" \
      -version "$VERSION" -platform "$p" -arch "$a" -source-rev "$rev" \
      ${ROOT:+-root "$ROOT"} ${TRUST_DIR:+-trust-dir "$TRUST_DIR"} 2>&1) || die "verification REFUSED for $asset: $out"
    printf '%s\n' "$out" | sed 's/^/    /'
    n=$((n+1))
  done < "$CAND/CANDIDATE.txt"
  echo "=== candidate SEALED: $n asset(s) verified, $(installers | wc -l | tr -d ' ') installer(s) present"
  ;;

stage)
  [ -d "$CAND" ] || die "no candidate at $CAND"
  [ -n "${RELEASE_REPO:-}" ] || die "set RELEASE_REPO=owner/name to stage (this repo has no remote by design)"
  command -v gh >/dev/null || die "gh is required to stage"
  sh packaging/release.sh attach
  tag="v$VERSION"
  rev=$(awk '$1=="source_rev"{print $2}' "$CAND/CANDIDATE.txt")
  target="${RELEASE_TARGET:-$rev}"
  [ "$target" = "$rev" ] || die "RELEASE_TARGET must equal the candidate's public source_rev"
  PUBLIC_DERIVE="${PUBLIC_DERIVE:-public-derive}"
  command -v "$PUBLIC_DERIVE" >/dev/null 2>&1 || die "public-derive is required to verify public source provenance"
  verified_target=$("$PUBLIC_DERIVE" guard-release -repo "$root" \
    -remote-url "https://github.com/$RELEASE_REPO.git" -tag "$tag" \
    -source-rev "$rev" -target "$target") || die "public source provenance refused; nothing was staged"
  [ "$verified_target" = "$rev" ] || die "public source provenance returned another source"

  public=$(public_files)
  if gh release view "$tag" --repo "$RELEASE_REPO" >/dev/null 2>&1; then
    state=$(gh release view "$tag" --repo "$RELEASE_REPO" --json isDraft -q .isDraft 2>/dev/null || echo unknown)
    [ "$state" = "true" ] || die "release $tag already exists and is NOT a draft — refusing to add assets to a public release"
    remote=$(gh release view "$tag" --repo "$RELEASE_REPO" --json assets -q '.assets[].name') \
      || die "cannot list the assets already in draft $tag"
    stray=$(printf '%s\n' "$remote" | while read -r f; do
      [ -n "$f" ] || continue
      [ "$f" = SHA256SUMS ] && continue
      printf '%s\n' "$public" | grep -qxF "$f" || echo "$f"
    done)
    [ -z "$stray" ] || die "draft $tag already holds assets this candidate does not carry; nothing was uploaded. Remove them by hand (gh release delete-asset $tag <name> --repo $RELEASE_REPO) and stage again:
$(printf '%s\n' "$stray" | sed 's/^/    /')"
    echo "=== reusing existing DRAFT $tag"
  else
    echo "=== create DRAFT $tag at $target (source $rev)"
    gh release create "$tag" --repo "$RELEASE_REPO" --draft --verify-tag \
       --title "STAGING $VERSION (not complete)" --notes "Candidate $VERSION (source $rev)"
  fi
  [ "$(gh release view "$tag" --repo "$RELEASE_REPO" --json isDraft -q .isDraft)" = "true" ] \
    || die "$tag is not a draft after creation — refusing to upload"

  (cd "$CAND" && printf '%s\n' "$public" | xargs sha256sum) > "$CAND/SHA256SUMS"
  for f in $public SHA256SUMS; do
    gh release upload "$tag" "$CAND/$f" --repo "$RELEASE_REPO" --clobber
    echo "    uploaded $f"
  done
  if [ -n "${RELEASE_NOTES:-}" ]; then
    [ -f "$RELEASE_NOTES" ] || die "RELEASE_NOTES names no file: $RELEASE_NOTES"
    gh release edit "$tag" --repo "$RELEASE_REPO" --notes-file "$RELEASE_NOTES"
    echo "    notes set from $RELEASE_NOTES"
  fi
  gh release edit "$tag" --repo "$RELEASE_REPO" --title "AII OS $VERSION"
  echo "    title set: AII OS $VERSION — READY TO PUBLISH (the operator's act)"
  cat <<MSG

=== staged as a DRAFT — not public.
Publishing is a deliberate act, after the qualification journeys:
  gh release edit $tag --repo $RELEASE_REPO --draft=false
MSG
  ;;

*) die "unknown phase '$phase' (prepare | attach | stage)" ;;
esac
