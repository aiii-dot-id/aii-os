third_party_licenses() {
  if [ -f go.mod ] && command -v "$1" >/dev/null 2>&1; then
    "$1" run ./cmd/aii-release licenses -go "$1" -o THIRD_PARTY_LICENSES || {
      echo "third-party licences: generation failed; no installer is built without them" >&2
      return 1
    }
  fi
  [ -s THIRD_PARTY_LICENSES ] || {
    echo "third-party licences: no THIRD_PARTY_LICENSES here and no source to generate it from; hand it off with the binaries" >&2
    return 1
  }
}
