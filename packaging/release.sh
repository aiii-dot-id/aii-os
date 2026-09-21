#!/bin/sh
# Assemble a release candidate. ONE authority, run HERE.
#
# WHY NOT CI: an installer is only valid where its platform's signing can
# be performed. macOS notarization needs Apple credentials and a Mac; a
# runner cross-compiling a bare darwin binary produces something that
# cannot be notarized, cannot be stapled, and cannot be installed.
# GitHub is the DISTRIBUTION surface, not the factory.
#
# WHY IT STOPS TWICE: signing is offline by doctrine — the
# platform_release key is held by AIII and never reaches a build host —
# and publication is a person's act. This script assembles and VERIFIES;
# it never signs and never publishes.
#
#   sh packaging/release.sh prepare   build, bind, emit payloads to sign
#   sh packaging/release.sh attach    VERIFY the returned signatures
#   sh packaging/release.sh stage     upload into a DRAFT release (with
#                                    SHA256SUMS; RELEASE_NOTES=<file> sets
#                                    the notes; RELEASE_TARGET=<sha> names
#                                    the distribution repo's commit)
#
# THE SCHEMA IS NOT WRITTEN HERE. It used to be: this script hand-wrote
# the signing payload, and every field disagreed with the verifier —
# wrong artifact kind, platform and arch missing, two forbidden fields
# present, and a "sha256:"-prefixed hash where bare hex was required. No
# signature it produced could ever have verified, and the bug survived
# because `attach` only checked that a .sig FILE EXISTED.
# Both halves now call internal/updates through aii-release.
set -e

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
OUT="${OUT:-dist}"
CAND="${CAND:-$OUT/candidate}"
GO="${GO:-${AII_GO:-go}}"
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo 0.0.0-dev)}"
phase="${1:-prepare}"
TOOL="$OUT/aii-release"

die() { echo "release: $*" >&2; exit 1; }

# Both roots are removed wholesale by prepare. An override is honoured
# only where build output can honestly live: relative, under this repo,
# no traversal. OUT=$HOME would have removed $HOME.
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

case "$phase" in
prepare)
  require_clean_tree
  rev=$(git rev-parse HEAD)
  echo "=== candidate $VERSION @ $(git rev-parse --short=12 HEAD)"
  # BOTH roots are cleared. A stale .dmg from a failed macOS handoff used
  # to survive here and be collected into the next candidate as though it
  # had just been built.
  rm -rf "$CAND" "$OUT"
  mkdir -p "$CAND" "$OUT"
  build_tool

  echo "=== updater archives (the assets the updater actually searches for)"
  # The candidate used to carry only installers — .deb, .exe, .dmg —
  # none of which the updater looks for, so even a correctly signed
  # release was undiscoverable. The names come from the shared contract.
  "$TOOL" assets -version "$VERSION" | while read -r asset; do
    case "$asset" in
      aii-os-app_*)     continue ;; # whole-bundle archive: build-dmg.sh produces it, we only inventory it
      *_linux_amd64*)   goos=linux;   goarch=amd64 ;;
      *_linux_arm64*)   goos=linux;   goarch=arm64 ;;
      *_macos_arm64*)   goos=darwin;  goarch=arm64 ;;
      *_windows_amd64*) goos=windows; goarch=amd64 ;;
      *) die "no build rule for asset $asset" ;;
    esac
    stage=$(mktemp -d); trap 'rm -rf "$stage"' EXIT
    bin="aii"; [ "$goos" = windows ] && bin="aii.exe"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO" build -trimpath \
      -ldflags "-s -w -X github.com/aiii-dot-id/aii-os/internal/app.Version=$VERSION" \
      -o "$stage/$bin" ./cmd/aii
    cp LICENSE "$stage/LICENSE" # the updater finds the binary by name and ignores the rest
    # The updater archive's own executable is signed too, before it is
    # archived, so its Authenticode signature is inside the bytes the
    # payload envelope then binds — the installer track already signs its
    # embedded binaries this way. A no-op unless a signer is configured.
    if [ "$goos" = windows ] && [ -n "${SIGN_CMD:-}" ]; then
      $SIGN_CMD "$stage/$bin" || die "signing $asset failed"
    fi
    case "$asset" in
      *.zip)    (cd "$stage" && zip -q "$root/$CAND/$asset" "$bin" LICENSE) ;;
      *.tar.gz) tar -czf "$CAND/$asset" -C "$stage" "$bin" LICENSE ;;
    esac
    rm -rf "$stage"; trap - EXIT
    echo "    $asset"
  done

  echo "=== native installers (what a person installs)"
  BUILD_MACOS=1 OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/build-all.sh || \
    die "packaging failed — a candidate is complete or it is not a candidate"
  for a in "$OUT"/*.deb "$OUT"/*.dmg "$OUT"/*.exe "$OUT"/aii-os-app_*.zip; do
    [ -e "$a" ] || continue
    cp "$a" "$CAND/" || die "copy $(basename "$a") into the candidate"
    echo "    $(basename "$a")"
  done

  echo "=== inventory: every supported target must be present"
  missing=0
  for asset in $("$TOOL" assets -version "$VERSION"); do
    [ -f "$CAND/$asset" ] || { echo "    MISSING $asset"; missing=$((missing+1)); }
  done
  # Installers are outside the updater's contract (it never fetches
  # them) but a candidate without them is not installable, and the old
  # inventory checked only the four archives this script had itself just
  # built — so it could not fail on a missing .dmg. Names are the
  # packagers' own.
  for inst in \
    "aii-os_${VERSION}_amd64.deb" \
    "aii-setup-${VERSION}-amd64.exe" \
    "AII-OS-${VERSION}.dmg"; do
    [ -f "$CAND/$inst" ] || { echo "    MISSING installer $inst"; missing=$((missing+1)); }
  done
  [ "$missing" -eq 0 ] || die "$missing required asset(s) missing"

  echo "=== bind every shipped executable to $rev"
  # The assertion's exit status IS the verdict. It used to be piped
  # through sed for indentation, and a pipeline's status is its last
  # command's — sed always succeeds — so a binary from the wrong commit
  # printed its refusal to stderr and the candidate proceeded. Capture,
  # then indent. And EVERY executable: the check covered the .deb and
  # the Linux tarballs and skipped the macOS tarball, the Windows archive
  # and installer, and both binaries inside the app bundle. Presence was
  # already enforced by the inventory,
  # so an unmatched glob here is a bug, not a case to skip.
  bind() {
    out=$(GO="$GO" sh packaging/assert-source-bound.sh "$1" "$rev" 2>&1) || die "$out"
    printf '%s\n' "$out" | sed 's/^/    /'
  }
  work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
  for deb in "$CAND"/*.deb; do
    rm -rf "$work/x"; mkdir -p "$work/x"; dpkg-deb -x "$deb" "$work/x"
    bind "$work/x/usr/bin/aii"
  done
  for tgz in "$CAND"/*.tar.gz; do
    rm -rf "$work/x"; mkdir -p "$work/x"; tar -xzf "$tgz" -C "$work/x"
    bind "$work/x/aii"
  done
  for z in "$CAND"/aii-os_*_windows_*.zip; do
    rm -rf "$work/x"; mkdir -p "$work/x"; unzip -q "$z" -d "$work/x"
    bind "$work/x/aii.exe"
  done
  for exe in "$CAND"/*.exe; do
    bind "$exe" # the installer is itself a Go executable from this tree; its payload is built by the same run
  done
  for appz in "$CAND"/aii-os-app_*.zip; do
    rm -rf "$work/x"; mkdir -p "$work/x"; unzip -q "$appz" -d "$work/x"
    bind "$work/x/AII OS.app/Contents/MacOS/aii"
    bind "$work/x/AII OS.app/Contents/MacOS/AII OS"
  done

  echo "=== emit signing payloads (rendered by aii-release, never by this script)"
  : > "$CAND/CANDIDATE.txt"
  echo "source_rev $rev" >> "$CAND/CANDIDATE.txt"
  echo "version $VERSION" >> "$CAND/CANDIDATE.txt"
  for asset in $("$TOOL" assets -version "$VERSION"); do
    case "$asset" in
      *_linux_amd64*)   p=linux;   a=amd64 ;;
      *_linux_arm64*)   p=linux;   a=arm64 ;;
      *_macos_arm64*)   p=macos;   a=arm64 ;;
      *_windows_amd64*) p=windows; a=amd64 ;;
      *) die "no payload rule for asset $asset — p/a would leak from the previous iteration" ;;
    esac
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

Return each envelope as $CAND/<asset>.platform.sig, then:
  sh packaging/release.sh attach
MSG
  ;;

attach)
  [ -d "$CAND" ] || die "no candidate at $CAND — run prepare first"
  build_tool
  rev=$(awk '$1=="source_rev"{print $2}' "$CAND/CANDIDATE.txt")
  [ -n "$rev" ] || die "candidate carries no source_rev"
  echo "=== VERIFY every asset the way the updater will"
  # Not a presence check. The old one asserted a .sig file existed and
  # that the artifact still hashed to a value in an UNSIGNED json file —
  # an empty signature satisfied it. This runs the real admission:
  # envelope, root, artifact kind, closed payload, binding and revocation.
  n=0
  while read -r asset p a; do
    [ "$asset" = "source_rev" ] || [ "$asset" = "version" ] && continue
    sig="$CAND/$asset.platform.sig"
    [ -f "$sig" ] || die "$asset has no signature"
    # The verifier signals REFUSAL only by exit status, and this line
    # used to pipe it through sed — whose status is always 0 — so a
    # wrong-root or stale-hash envelope printed its refusal indented and
    # the candidate was announced SEALED.
    # Same defect as the binding pipe above, one phase later. Capture,
    # then indent.
    out=$("$TOOL" verify -artifact "$CAND/$asset" -sig "$sig" \
      -version "$VERSION" -platform "$p" -arch "$a" -source-rev "$rev" \
      ${ROOT:+-root "$ROOT"} ${TRUST_DIR:+-trust-dir "$TRUST_DIR"} 2>&1) || die "verification REFUSED for $asset: $out"
    printf '%s\n' "$out" | sed 's/^/    /'
    n=$((n+1))
  done < "$CAND/CANDIDATE.txt"
  [ "$n" -gt 0 ] || die "candidate lists no assets"
  echo "=== candidate SEALED: $n asset(s) verified"
  ;;

stage)
  [ -d "$CAND" ] || die "no candidate at $CAND"
  [ -n "${RELEASE_REPO:-}" ] || die "set RELEASE_REPO=owner/name to stage (this repo has no remote by design)"
  command -v gh >/dev/null || die "gh is required to stage"
  sh packaging/release.sh attach   # never stage an unsealed candidate
  tag="v$VERSION"
  rev=$(awk '$1=="source_rev"{print $2}' "$CAND/CANDIDATE.txt")
  # The release points at the commit the DISTRIBUTION repository carries
  # for this source. A derived public repository has its own history, so
  # its commit for source_rev is named by RELEASE_TARGET; unset, the
  # source commit itself must exist in the release repository.
  target="${RELEASE_TARGET:-$rev}"

  # REFUSE A PUBLIC RELEASE. `gh release create ... || true` used to
  # swallow the failure when a release already existed — and then upload
  # into it. If that release was public, assets landed in public without
  # anyone deciding to publish them.
  if gh release view "$tag" --repo "$RELEASE_REPO" >/dev/null 2>&1; then
    state=$(gh release view "$tag" --repo "$RELEASE_REPO" --json isDraft -q .isDraft 2>/dev/null || echo unknown)
    [ "$state" = "true" ] || die "release $tag already exists and is NOT a draft — refusing to add assets to a public release"
    echo "=== reusing existing DRAFT $tag"
  else
    echo "=== create DRAFT $tag at $target (source $rev)"
    gh release create "$tag" --repo "$RELEASE_REPO" --draft --target "$target" \
       --title "STAGING $VERSION (not complete)" --notes "Candidate $VERSION (source $rev)"
  fi
  # Verify it really is a draft before writing anything into it.
  [ "$(gh release view "$tag" --repo "$RELEASE_REPO" --json isDraft -q .isDraft)" = "true" ] \
    || die "$tag is not a draft after creation — refusing to upload"

  # SHA256SUMS: the convention people verify a download with
  # (`sha256sum -c SHA256SUMS --ignore-missing`). The .platform.sig
  # files are the updater's proof; this is the person's.
  (cd "$CAND" && ls | grep -v -e '\.payload\.json$' -e '^CANDIDATE\.txt$' -e '^SHA256SUMS$' | xargs sha256sum) > "$CAND/SHA256SUMS"
  for a in "$CAND"/*; do
    case "$a" in *.payload.json|*CANDIDATE.txt) continue ;; esac
    gh release upload "$tag" "$a" --repo "$RELEASE_REPO" --clobber
    echo "    uploaded $(basename "$a")"
  done
  if [ -n "${RELEASE_NOTES:-}" ]; then
    [ -f "$RELEASE_NOTES" ] || die "RELEASE_NOTES names no file: $RELEASE_NOTES"
    gh release edit "$tag" --repo "$RELEASE_REPO" --notes-file "$RELEASE_NOTES"
    echo "    notes set from $RELEASE_NOTES"
  fi
  # The draft carried a staging title while assets, sums and notes were
  # still landing (0.1.2 was published mid-stage, and its SHA256SUMS
  # arrived 35 s after publication). The real title is the last write:
  # a draft that reads "STAGING" is not ready, whatever its asset count.
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
