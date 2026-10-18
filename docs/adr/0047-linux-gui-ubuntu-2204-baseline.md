# 0047: Linux GUI uses the Ubuntu 22.04 GTK3 baseline

Status: Accepted.

Context: Wails v3 beta.27 supports both its newer GTK4/WebKitGTK 6.0 backend
and the `gtk3` build tag, which links GTK3 and WebKitGTK 4.1. Building the
latter on an advancing `ubuntu-latest` image raised the glibc floor of the
td-gui AppImage and made it unavailable on Ubuntu 22.04 systems.

Decision: Build Linux td-gui with `-tags gui,gtk3` on native Ubuntu 22.04
runners. The Linux .deb declares `libgtk-3-0` and `libwebkit2gtk-4.1-0` as
runtime dependencies. Release packaging runs on `ubuntu-22.04` and
`ubuntu-22.04-arm`, preserving x86_64 and arm64 artifacts at the same glibc
baseline. AppImages embed architecture-specific GitHub Releases zsync update
information and publish the matching `.AppImage.zsync` sidecar.

Every Linux AppImage is accepted only after an artifact-level check verifies
public executable/traversable permissions and desktop metadata, then starts
AppRun as the non-root runner in an isolated network namespace under Xvfb,
requires a visible window, and retains a screenshot. The check runs before
release upload.

Consequences: Linux GUI development and packaging need the Jammy GTK3 and
WebKitGTK 4.1 development packages. The Wails `gtk3` tag is Linux-specific;
macOS and Windows continue to use their native Wails webviews. The existing
CGO-free CLI gates and application logic are unchanged.
