# Resolve one Go toolchain and the supported non-cgo build matrix for all qualification scripts. Sourced by the runners; not a standalone test.
export GOTOOLCHAIN=local
if [ -n "${AII_GO:-}" ]; then
	GO_ASKED=$AII_GO
elif [ -e /opt/go-current/bin/go ]; then
	GO_ASKED=/opt/go-current/bin/go
else
	GO_ASKED=$(command -v go || echo go)
fi
GO=$(readlink -f "$GO_ASKED" 2>/dev/null || true)
[ -n "$GO" ] && [ -x "$GO" ] || GO=$GO_ASKED
case $GO in */*) PATH="$(dirname "$GO"):$PATH"; export PATH ;; esac
ROWS="linux/amd64 linux/arm64 darwin/arm64 windows/amd64 android/arm64"
