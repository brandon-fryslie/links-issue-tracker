#!/usr/bin/env bash
#
# install.sh — install `lit` from one of three sources:
#
#   (default)              build from this checkout's source (go build, ldflag-stamped)
#   --from-release <tag>   download the published release archive for that tag
#   --latest-release       download the latest published release archive
#
# All modes write to the same target directory (the dir on $PATH that already
# owns a `lit` if any; otherwise $GOBIN) and run the same stale-binary
# detector. Switching modes is a single flag, not a different script.
#
# [LAW:single-enforcer] One installer, one target-resolution rule, one stale
# detector. The "what to install" varies; "where + safety checks" do not.
# [LAW:one-source-of-truth] Source builds inject version/commit/date via
# ldflags so `lit version` reports something meaningful even for ad-hoc
# checkouts; release-download mode trusts the prebuilt binary's already-baked
# stamps (set by goreleaser).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

REPO_DOWNLOAD_BASE="https://github.com/brandon-fryslie/links-issue-tracker/releases/download"
REPO_LATEST_API="https://api.github.com/repos/brandon-fryslie/links-issue-tracker/releases/latest"

mode="source"
release_tag=""
while [ $# -gt 0 ]; do
    case "$1" in
        --from-release)
            mode="release"
            release_tag="${2:-}"
            if [ -z "$release_tag" ]; then
                echo "error: --from-release requires a tag (e.g. v0.1.0)" >&2
                exit 2
            fi
            shift 2
            ;;
        --latest-release)
            mode="latest"
            shift
            ;;
        -h|--help)
            sed -n '3,15p' "$0"
            exit 0
            ;;
        *)
            echo "error: unknown flag: $1" >&2
            echo "usage: $0 [--from-release <tag>|--latest-release]" >&2
            exit 2
            ;;
    esac
done

# --- target-dir resolution: identical across all modes -----------------------

GOBIN="${GOBIN:-$(go env GOBIN 2>/dev/null || true)}"
GOBIN="${GOBIN:-$(go env GOPATH 2>/dev/null | cut -d: -f1)/bin}"

TARGET_DIR=""
if command -v lit >/dev/null 2>&1; then
    EXISTING="$(command -v lit)"
    # Resolve symlinks so we update the real file, not a dangling link.
    REAL_EXISTING="$(readlink -f "$EXISTING" 2>/dev/null || python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$EXISTING")"
    TARGET_DIR="$(dirname "$REAL_EXISTING")"
fi
TARGET_DIR="${TARGET_DIR:-$GOBIN}"
mkdir -p "$TARGET_DIR"

# --- mode dispatch -----------------------------------------------------------

case "$mode" in
    source)
        # ldflag-stamp the build so `lit version` reports meaningful identity
        # for source builds (releases stamp via goreleaser; this is the
        # equivalent for ad-hoc checkouts).
        ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
        commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
        date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        pkg="github.com/bmf/links-issue-tracker/internal/version"
        GOFLAGS="${GOFLAGS:+$GOFLAGS }-buildvcs=false" go build \
            -ldflags "-X ${pkg}.Version=${ver} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}" \
            -o "$TARGET_DIR/lit" ./cmd/lit
        ;;
    release|latest)
        if [ "$mode" = "latest" ]; then
            # Resolve "latest" to a concrete tag via the GitHub API.
            release_tag="$(curl -fsSL "$REPO_LATEST_API" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])')"
            if [ -z "$release_tag" ]; then
                echo "error: could not resolve latest release tag from $REPO_LATEST_API" >&2
                exit 1
            fi
            echo "Latest release: $release_tag"
        fi

        # Resolve current platform → goreleaser archive name (must mirror
        # .goreleaser.yml's name_template: lit_<version>_<goos>_<goarch>.<ext>).
        os="$(uname -s | tr '[:upper:]' '[:lower:]')"
        arch_raw="$(uname -m)"
        case "$arch_raw" in
            x86_64|amd64)  arch="amd64" ;;
            arm64|aarch64) arch="arm64" ;;
            *) echo "error: unsupported architecture: $arch_raw" >&2; exit 1 ;;
        esac
        case "$os" in
            linux|darwin) ext="tar.gz" ;;
            mingw*|msys*|cygwin*) os="windows"; ext="zip" ;;
            *) echo "error: unsupported OS: $os" >&2; exit 1 ;;
        esac
        archive="lit_${release_tag}_${os}_${arch}.${ext}"
        url="${REPO_DOWNLOAD_BASE}/${release_tag}/${archive}"
        checksums_url="${REPO_DOWNLOAD_BASE}/${release_tag}/checksums.txt"

        # Download to a temp dir, verify SHA256, extract, atomic-rename into place.
        tmp="$(mktemp -d)"
        trap 'rm -rf "$tmp"' EXIT
        echo "Downloading $url ..."
        curl -fsSL -o "$tmp/$archive" "$url"
        echo "Downloading checksums ..."
        curl -fsSL -o "$tmp/checksums.txt" "$checksums_url"

        expected="$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')"
        if [ -z "$expected" ]; then
            echo "error: $archive not found in checksums.txt" >&2
            exit 1
        fi
        # sha256sum (Linux) or shasum -a 256 (macOS).
        if command -v sha256sum >/dev/null 2>&1; then
            actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
        else
            actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
        fi
        if [ "$actual" != "$expected" ]; then
            echo "error: SHA256 mismatch for $archive" >&2
            echo "  expected: $expected" >&2
            echo "  actual:   $actual" >&2
            exit 1
        fi
        echo "Checksum OK."

        # Extract into the temp dir; goreleaser archives contain a top-level `lit` binary.
        if [ "$ext" = "tar.gz" ]; then
            tar -xzf "$tmp/$archive" -C "$tmp"
        else
            unzip -q "$tmp/$archive" -d "$tmp"
        fi
        if [ ! -x "$tmp/lit" ]; then
            echo "error: extracted archive did not contain a 'lit' binary" >&2
            exit 1
        fi

        # Atomic rename into place (same filesystem as $TARGET_DIR).
        mv "$tmp/lit" "$TARGET_DIR/lit.new"
        mv -f "$TARGET_DIR/lit.new" "$TARGET_DIR/lit"
        ;;
esac

# Stale `lnks` symlink/binary from previous installs is removed; `lit` is the
# only entrypoint going forward.
rm -f "$TARGET_DIR/lnks"

# Detect any *other* `lit` on PATH that we did NOT just overwrite — those are
# the stale binaries that cause "the fix landed but the bug came back" reports.
STALE=()
IFS=':' read -r -a PATH_ENTRIES <<< "$PATH"
for dir in "${PATH_ENTRIES[@]}"; do
    [ -z "$dir" ] && continue
    candidate="$dir/lit"
    [ -x "$candidate" ] || continue
    real="$(readlink -f "$candidate" 2>/dev/null || python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$candidate")"
    if [ "$real" != "$TARGET_DIR/lit" ]; then
        STALE+=("$candidate")
    fi
done

echo "Installed lit -> $TARGET_DIR/lit"
"$TARGET_DIR/lit" version 2>/dev/null || true
if [ "${#STALE[@]}" -gt 0 ]; then
    echo
    echo "WARNING: other 'lit' binaries found on PATH that were NOT updated:"
    for s in "${STALE[@]}"; do echo "  $s"; done
    echo "Remove them or shadow them, or future fixes will not reach the binary you actually run."
fi
