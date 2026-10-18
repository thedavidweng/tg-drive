# Release size accounting

Measured from published v1.0.1 and v1.0.2 assets on 2026-10-18. The full
platform baseline below is v1.0.1; the v1.0.2 follow-up uses published
macOS, Linux and Windows artifacts. The v1.0.1 release source is
`09bae9da081b8b32bd634721a75e44504334fc25`; successful packaging run:
[39054312774](https://github.com/thedavidweng/tg-drive/actions/runs/39054312774).
All 16 CLI checksum entries and all six GUI SHA-256 sidecars matched the
downloaded bytes. SHA-256 agreement verifies content against the published
manifests; it is separate from verifying the cosign identity.

## Published v1.0.2 macOS follow-up

Release source: `f76f77d40fa1c8b4d67a569a4625b4d059d46e71`; successful
packaging run:
[39127115752](https://github.com/thedavidweng/tg-drive/actions/runs/39127115752).
All three CLI downloads match `checksums.txt`; the GUI DMG matches its
published SHA-256 sidecar. Values are bytes, measured using the same
regular-file payload accounting as the baseline.

| Artifact | Download | Main executable | Unpacked payload |
| --- | ---: | ---: | ---: |
| `td_darwin_arm64.tar.gz` | 15,124,415 | 51,336,096 | 51,411,609 |
| `td_darwin_x86_64.tar.gz` | 16,746,742 | 54,101,344 | 54,176,857 |
| `td_darwin_universal.tar.gz` | 31,841,929 | 105,468,832 | 105,544,345 |
| `td-gui_1.0.2_darwin_universal.dmg` | 40,487,517 | 127,221,040 | 127,753,507 |

The signed ARM64 CLI download is 52.5% smaller than the v1.0.2 Universal
download; x86-64 is 47.4% smaller. These are published complete archives,
including the accompanying documentation, rather than locally compressed
`lipo` slices. The Universal CLI download decreased by only 3,907 bytes from
v1.0.1; retaining thin downloads accounts for the meaningful architecture
savings. The GUI executable and App payload sizes are unchanged.

Each CLI executable passes `codesign --verify --strict`, and all three
`td version --json` invocations report v1.0.2 and the release source above.
The x86-64 invocation ran through this ARM host's translation support; it is
not a native Intel acceptance result. The GUI App passes signature
verification for both architectures, and both the App and DMG pass
`stapler validate`. The copied GUI process remained alive for five seconds
using isolated configuration and index paths.

The host reports `spctl --status: assessments disabled`.
`spctl --assess` on the CLI executables reports
`source=Notarized Developer ID` together with `override=security disabled`.
This is signature/notarization and local-startup evidence, not clean-machine
Gatekeeper enforcement or interactive install/update/uninstall acceptance.
Bare CLI executables have no stapled ticket; Apple's
[notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)
explains that standalone binaries cannot currently carry a stapled ticket.

Windows signing and native installation remain unaccepted. Direct inspection
of the published v1.0.2 installer, its extracted `td-gui.exe`, and both CLI
ZIP executables found PE security-directory offset and size `0/0`; green
Windows CI does not supply
an Authenticode certificate, trust chain, or timestamp. Linux native package
acceptance, package-manager install/update paths, secured macOS enforcement,
and native Intel/Windows installation remain tracked in #124 and #106.

## Published v1.0.2 Linux and Windows follow-up

All 38 published asset byte counts and SHA-256 digests match the GitHub
release metadata. All 20 checksum-manifest entries and all eight GUI checksum
sidecars match; all seven archive SPDX-2.3 documents have valid internal
relationship references. These checks establish distribution-file consistency;
cosign certificate-identity verification is separate. The API reports
`immutable=false` for release ID `416435419`; no published tag or asset was
replaced during acceptance.

The same regular-file accounting used above gives the following bytes.
Both Linux architectures and every CLI deb/rpm/apk format remain present.
APK payloads include the control record; AppImages include bundled runtime
libraries. Measurements inspect the published files without running them.

| Artifact | Download | Main executable | Unpacked payload |
| --- | ---: | ---: | ---: |
| `td_linux_arm64.tar.gz` | 14,659,419 | 49,873,058 | 49,948,571 |
| `td_linux_x86_64.tar.gz` | 15,986,850 | 52,187,298 | 52,262,811 |
| `td_windows_arm64.zip` | 14,646,936 | 50,224,128 | 50,299,641 |
| `td_windows_x86_64.zip` | 16,297,878 | 53,327,872 | 53,403,385 |
| `td-gui_1.0.2_linux_amd64.deb` | 21,065,714 | 65,074,032 | 65,276,372 |
| `td-gui_1.0.2_linux_arm64.deb` | 19,670,836 | 62,494,456 | 62,696,796 |
| `td_1.0.2_linux_amd64.deb` | 15,971,592 | 52,187,298 | 52,187,298 |
| `td_1.0.2_linux_arm64.deb` | 14,635,858 | 49,873,058 | 49,873,058 |
| `td_1.0.2_linux_amd64.apk` | 16,708,950 | 52,187,298 | 52,187,598 |
| `td_1.0.2_linux_arm64.apk` | 15,266,733 | 49,873,058 | 49,873,359 |
| `td_1.0.2_linux_amd64.rpm` | 15,965,652 | 52,187,298 | 52,187,298 |
| `td_1.0.2_linux_arm64.rpm` | 14,640,529 | 49,873,058 | 49,873,058 |
| `td-gui_1.0.2_linux_aarch64.AppImage` | 93,927,944 | 62,625,528 | 313,671,768 |
| `td-gui_1.0.2_linux_x86_64.AppImage` | 97,106,424 | 65,082,224 | 318,790,430 |

Compared with v1.0.1, Linux CLI executable bytes decrease by 98,304 on
x86-64 and 65,536 on ARM64; Windows CLI decreases by 96,768 and 96,256.
Linux GUI deb executables change by +28,736 / -8,160 bytes on x86-64 / ARM64.
The x86-64 AppImage payload grows by 11,597,192 bytes while ARM64 shrinks by
6,857,662 bytes; these bundled payload changes cannot be attributed to the
small GUI executable changes alone. No runtime library was removed and no
compression/parity benefit is inferred from these totals.

## Published v1.0.2 standalone installation

The unmodified release-source `install.sh`, Git blob
`02467bbc15adc63bdad3a65c56af0a7e3583af6d`, passed fresh installation,
`td version --json`, v1.0.1 -> v1.0.2 overwrite upgrade, and uninstall with
binary removal on macOS ARM64 and native Ubuntu 24.04.4 LTS x86-64.
Homebrew was absent from each test PATH; install/config/cache/data paths were
isolated in temporary directories. The prior v1.0.1 binary and installed
v1.0.2 binary were checked against their published archive contents.

| Platform / downloaded executable | Installed v1.0.2 SHA-256 |
| --- | --- |
| macOS Universal (ARM64 host) | `3f9b0b36954210427b4437c4b5a2e6dbe77a4431cae31a7cd9b1a30a00e9e858` |
| Linux x86-64 | `11cc4868df6c47f2766de653db471581bdf59563d405738d06ee2c13f90770be` |

Both report v1.0.2 and release source
`f76f77d40fa1c8b4d67a569a4625b4d059d46e71`. On native Linux,
`td completion bash`, `td completion zsh`, and `td completion fish` each
exit 0 with nonempty output. Tests removed the temporary files. This does
not establish Linux GUI/ARM64/package-manager acceptance, native Windows or
Intel Mac acceptance, secured-host Gatekeeper enforcement, interactive GUI
behavior, or authenticated Telegram acceptance.

## Published v1.0.1 downloads and payloads

All values are bytes, not MiB. Unpacked bytes sum regular-file logical sizes,
excluding symlink targets and filesystem block allocation. Archive rows include
the bundled README, licenses and changelog; installing only `td` consumes the
binary column. Linux package payloads include their files under `/usr`; APK
also carries a small `.PKGINFO` record which is not an installed file. AppImage
payloads include their bundled runtime libraries. A mounted DMG contains the
App plus an Applications symlink; the payload column measures the App.

| Artifact | Download | Main executable | Unpacked payload |
| --- | ---: | ---: | ---: |
| `td-gui_1.0.1_linux_aarch64.AppImage` | 91,908,616 | 62,633,688 | 320,529,430 |
| `td-gui_1.0.1_linux_amd64.deb` | 21,064,514 | 65,045,296 | 65,247,636 |
| `td-gui_1.0.1_linux_arm64.deb` | 19,668,116 | 62,502,616 | 62,704,956 |
| `td-gui_1.0.1_linux_x86_64.AppImage` | 95,111,672 | 65,053,488 | 307,193,238 |
| `td-gui_1.0.1_windows_x86_64-installer.exe` | 22,429,266 | 66,301,440 | 68,198,419 |
| `td_1.0.1_linux_amd64.apk` | 16,715,333 | 52,285,602 | 52,285,902 |
| `td_1.0.1_linux_amd64.deb` | 15,972,326 | 52,285,602 | 52,285,602 |
| `td_1.0.1_linux_amd64.rpm` | 15,973,665 | 52,285,602 | 52,285,602 |
| `td_1.0.1_linux_arm64.apk` | 15,293,532 | 49,938,594 | 49,938,895 |
| `td_1.0.1_linux_arm64.deb` | 14,637,486 | 49,938,594 | 49,938,594 |
| `td_1.0.1_linux_arm64.rpm` | 14,636,985 | 49,938,594 | 49,938,594 |
| `td_darwin_universal.tar.gz` | 31,845,836 | 105,665,632 | 105,739,894 |
| `td_linux_arm64.tar.gz` | 14,660,792 | 49,938,594 | 50,012,856 |
| `td_linux_x86_64.tar.gz` | 15,990,696 | 52,285,602 | 52,359,864 |
| `td_windows_arm64.zip` | 14,646,996 | 50,320,384 | 50,394,646 |
| `td_windows_x86_64.zip` | 16,300,385 | 53,424,640 | 53,498,902 |
| `td-gui_1.0.1_darwin_universal.dmg` | 40,487,526 | 127,221,040 | 127,753,507 |

The Windows NSIS extracted payload includes temporary installer plugins and the
WebView2 bootstrapper. The installed application consists of `td-gui.exe` and
the generated uninstaller. The extracted application and uninstaller sum to
66,356,093 bytes; this is a payload measurement, not a measured Windows
installation. WebView2 runtime installation and subsequent user data are
separate system/user costs, not bytes shipped inside the GUI executable.

Linux CLI packages contain the same executable bytes for a given architecture.
Linux GUI deb/AppImage executable sizes differ because linuxdeploy adjusts ELF
runtime search paths during bundling. The Windows installer stub is an x86 PE;
its application is an x86-64 PE. Windows CLI ZIPs are separate x86-64/ARM64.
The v1.0.1 macOS CLI and GUI are both Universal x86-64/ARM64.

Extracting each architecture with `lipo -thin` gives the following comparison.
The gzip column is a locally created archive containing only that executable,
not a published download, signed replacement or complete GUI App bundle.

| Published Universal executable | Architecture | Thin executable bytes | Binary-only tar.gz bytes |
| --- | --- | ---: | ---: |
| CLI | ARM64 | 51,434,592 | 14,730,831 |
| CLI | x86-64 | 54,204,544 | 16,442,126 |
| GUI | ARM64 | 62,045,488 | 18,878,379 |
| GUI | x86-64 | 65,149,856 | 20,713,425 |

The ARM64 CLI slice is 51.3% smaller than the Universal executable. Actual new
archive sizes must be measured after building, signing and packaging each thin
binary independently. The Universal GUI DMG remains the supported macOS GUI
download; thinning its executable here does not establish a valid thin App.

## Architecture and build settings

The v1.0.1 release publishes only `td_darwin_universal.tar.gz`. The release
configuration now retains that name and additionally creates
`td_darwin_x86_64.tar.gz` and `td_darwin_arm64.tar.gz`. Both thin binaries and
the Universal binary are signed and notarized before archiving. The original
Universal consumers in `install.sh` and the pull-based Homebrew tap remain valid.
The Linux archive and deb/rpm/apk matrices remain enabled; no cask is generated
by this repository, so changes cannot overwrite a tap completion stanza.

CLI release builds now use `-trimpath`, matching local builds. `CGO_ENABLED=0`,
version linker variables, `GOMEMLIMIT=3GiB`, the serialized release build,
cosign checksum signing, and archive SBOM generation remain in place. The
archive/SBOM/checksum rules apply to all seven CLI archives, including the two
new macOS archives. The v1.0.2 measurements and signature checks above verify
the published macOS downloads. Package-manager and remaining native
installation acceptance must be completed before #124 can close.

## CLI dependency attribution and startup

`go version -m` on the published CLI records Go 1.26.0, `modernc.org/sqlite`
v1.60.1 and `github.com/gotd/td` v0.162.0. Reachable `go list -deps -json ./cmd/td`
includes both the SQLite adapter and the Telegram client. The release is
stripped, so function ranges were recovered from its Go `.gopclntab` using
the standard library's `debug/elf` and `debug/gosym`, without rebuilding it.
The Linux x86-64 executable contains 21,903,825 bytes of `.text`; 74,413 Go
function ranges account for 21,903,809 bytes:

| Function name prefix | Function text bytes |
| --- | ---: |
| `github.com/gotd/td` | 12,945,792 |
| `modernc.org/sqlite` | 2,154,496 |
| `modernc.org/libc` | 105,088 |
| `github.com/thedavidweng/tg-drive` | 763,648 |
| `golang.org/x` | 83,552 |
| Runtime, standard library and remaining modules | 5,851,233 |

These ranges include alignment and exclude read-only data, writable data, Go
metadata and signatures. Inlined code follows its enclosing function. They
attribute executable text, not total module footprint or the savings from
removing a module. SQLite's compile-memory cost is not evidence that it is the
largest shipped code contributor; Telegram's generated client is larger in
this measurement. Neither dependency is removed or replaced.

On the shared ARM64 macOS acceptance host, five launches of the published
Universal CLI had median elapsed times of 78.5 ms (`version`), 84.2 ms
(`completion bash`), 328.8 ms (`completion zsh`) and 151.0 ms (`completion fish`).
One `/usr/bin/time -l td version` run recorded a maximum RSS of 28,934,144 bytes.
These are first-then-warm launches under shared machine load, not controlled
cold-cache measurements or a before/after optimization claim. A new full-target
comparison build was deferred because of shared host memory pressure.

## Frontend and native packaging

A frozen-lockfile Bun/Vite build of the same frontend source as v1.0.1 emits
9,662,387 bytes: JS 4,960,105; CSS 54,603; WASM 4,646,932; HTML 747.
Largest emitted files:

| File family | Bytes |
| --- | ---: | ---: |
| PDFium WASM | 4,646,932 |
| PDF viewer JS | 1,081,984 |
| PowerPoint viewer JS | 996,230 |
| ExcelJS | 929,569 |
| PDF worker engine JS | 689,501 |
| Main application JS | 454,977 |
| PDF direct engine JS | 301,583 |

There are no byte-identical emitted files. Preview code is already split into
lazy chunks. PDFium is required by PDF previews and is embedded once per native
architecture; a Universal executable naturally carries two copies. Chunk
splitting changes loading behavior but does not remove embedded bytes. No
preview feature or dependency is removed without a behavior-parity proof.

The Linux AppImage uses SquashFS ZSTD and bundles its GTK runtime stack. The deb
uses host shared libraries, which explains much of the download/footprint
difference; the formats have different installation prerequisites.
AppImage `usr/lib` accounts for 239,842,798 bytes on x86-64 and 255,663,986 on
ARM64; `usr/bin` accounts for 65,053,488 and 62,633,688 bytes respectively.
The remaining payload consists mostly of `usr/share` (2,061,590 / 1,992,586
bytes), icons and launch hooks. No bundled runtime library was removed.
The macOS DMG uses zlib compression; Windows NSIS currently uses Deflate. No compression
setting is changed without a controlled packaging-time/download comparison.
UPX remains unadopted: none of these measurements establishes a Linux startup,
RSS and antivirus/integrity benefit that would justify another transformation.

## Signing acceptance

The published macOS CLI passes strict signature and online notarization-ticket
verification. The App and DMG pass strict signature checks and stapler validation.
Both release submissions were accepted by Apple. The local `spctl` results
report `Notarized Developer ID` with `override=security disabled`; this host
therefore does not prove enforcement on a fresh Gatekeeper-enabled installation.
Both architectures pass signature verification. The published CLI also runs
under Rosetta. The downloaded GUI process started with isolated configuration,
database and session paths, remained alive for five seconds, and was terminated
by the acceptance worker. This is process startup evidence only; interactive
UI, login and transfer acceptance were not performed.

The published Windows installer and its embedded `td-gui.exe` both have zero
PE security-directory offset and size: neither carries Authenticode. The
Windows release workflow has no signing step. macOS acceptance does not
complete #106; it remains open for a Windows signing identity, timestamped
application/installer signatures and native installation/verification.

## Reproduction

Download each tag with `gh release download <tag> -R thedavidweng/tg-drive`,
verify `checksums.txt` and the GUI `.sha256` files, then inspect archive members
with `tar`/`7zz`. Extract deb ar members before extracting `data.tar.*`; macOS
`tar` can also read the rpm payload. `7zz` reads the AppImage SquashFS and NSIS
installer without executing either. Count regular files without following
symlinks. Mount the DMG read-only and count `td-gui.app`.

Use `file`/`lipo -archs` for architecture, `go version -m` for Go build settings,
`go list -deps -json ./cmd/td` for reachable dependencies. `go tool nm -size`
requires an unstripped diagnostic build; the published stripped ELF instead
retains Go function ranges in `.gopclntab`. Use
`make gui-frontend-build` and sum `frontend/dist` files for the frontend.
Use `codesign --verify --deep --strict`, `xcrun stapler validate`, and the online
`codesign --check-notarization -R "=notarized"` requirement for macOS evidence.

For standalone install reproduction, obtain `install.sh` from the release
source above and check its Git blob. Set `TD_INSTALL_DIR` and XDG paths to
an isolated temporary directory, keep Homebrew out of PATH, run `sh install.sh`
and the installed `td version --json`, and compare the binary hash with the
checksum-verified archive executable. Run `sh install.sh uninstall` and assert
binary removal. Repeat with the verified v1.0.1 executable seeded in the install
directory before running the installer. The installer resolves the latest
release, so the reported tag/source/hash must be recorded for every run.
