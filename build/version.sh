# Sourced by the GUI packaging scripts. VERSION_LDFLAGS stamps td-gui the
# way .goreleaser.yaml stamps td, so Settings.Versions reports the release
# version; verify_version checks a packaged binary reports it.
# Requires VERSION (the release version without a leading v).

VERSION_PKG=github.com/thedavidweng/tg-drive/internal/version
VERSION_COMMIT="$(git rev-parse HEAD 2>/dev/null || echo none)"
VERSION_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VERSION_LDFLAGS="-X $VERSION_PKG.Version=$VERSION -X $VERSION_PKG.Commit=$VERSION_COMMIT -X $VERSION_PKG.Date=$VERSION_DATE -X $VERSION_PKG.BuiltBy=package.sh"

# verify_version BINARY: fail unless `BINARY --version` prints $VERSION.
verify_version() {
	local got
	if ! got="$("$1" --version | tr -d '\r')"; then
		echo "package: cannot run $1 --version" >&2
		return 1
	fi
	if [ "$got" != "$VERSION" ]; then
		echo "package: $1 reports version '$got', want '$VERSION'" >&2
		return 1
	fi
	echo "package: $1 reports version $got"
}
