// mkmanifest emits the per-release manifest goreleaser publishes alongside
// the binary artifacts. It is invoked from .goreleaser.yml as a release-time
// hook: goreleaser builds the per-platform archives, computes SHA256 sums
// (dist/checksums.txt), then this tool reads those bytes and writes
// release-manifest.json — which goreleaser then uploads as an extra release
// asset.
//
// The tool is deliberately a separate program (not a goreleaser plugin /
// template) because the schema lives in internal/release, and emitting the
// same Go type both at build time AND read time is the [LAW:one-source-of-truth]
// discipline that prevents the manifest format from drifting between producer
// and consumer.
//
// Invocation:
//
//	mkmanifest \
//	  -version v0.1.0 \
//	  -commit abcdef0 \
//	  -date 2026-05-24T15:21:00Z \
//	  -dist ./dist \
//	  -base-url https://github.com/owner/repo/releases/download \
//	  -out ./dist/release-manifest.json
//
// The `-dist` directory must contain goreleaser's `checksums.txt` and the per-
// platform archive files referenced therein.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmf/links-issue-tracker/internal/release"
	"github.com/bmf/links-issue-tracker/internal/store/migrations"
	"github.com/bmf/links-issue-tracker/internal/version"
)

func main() {
	var (
		ver     = flag.String("version", "", "release version (e.g. v0.1.0); required")
		commit  = flag.String("commit", "", "git short SHA of the release commit; required")
		date    = flag.String("date", "", "RFC3339 build timestamp; required")
		distDir = flag.String("dist", "dist", "goreleaser dist directory")
		baseURL = flag.String("base-url", "", "release asset download URL prefix (no trailing slash); required")
		outPath = flag.String("out", "", "output path for release-manifest.json; required")
	)
	flag.Parse()

	for name, val := range map[string]string{
		"-version":  *ver,
		"-commit":   *commit,
		"-date":     *date,
		"-base-url": *baseURL,
		"-out":      *outPath,
	} {
		if strings.TrimSpace(val) == "" {
			die("required flag %s missing", name)
		}
	}

	max, err := migrations.MaxVersion()
	if err != nil {
		die("read migration registry: %v", err)
	}

	artifacts, err := collectArtifacts(*distDir, strings.TrimRight(*baseURL, "/"), *ver)
	if err != nil {
		die("collect artifacts: %v", err)
	}

	manifest := release.Manifest{
		Info: version.Info{
			Version: *ver,
			Commit:  *commit,
			Date:    *date,
			IsDev:   false, // releases are by definition not dev
			Schema:  version.SchemaSupport{Min: migrations.Baseline, Max: max},
		},
		Artifacts: artifacts,
	}

	out, err := os.Create(*outPath)
	if err != nil {
		die("create %s: %v", *outPath, err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&manifest); err != nil {
		die("encode manifest: %v", err)
	}
}

// collectArtifacts reads goreleaser's checksums.txt and emits one
// release.Artifact per line. The producer is goreleaser; its file format is
// "<sha256>  <filename>" — we accept exactly that shape and reject everything
// else (the enumeration-gap rule: parse the producer's actual output, not a
// looser superset).
//
// The Artifact.Platform is derived from the filename: goreleaser writes
// archives named like "lit_v0.1.0_darwin_arm64.tar.gz"; we strip the version
// and extract the GOOS_GOARCH segment.
func collectArtifacts(distDir, baseURL, ver string) ([]release.Artifact, error) {
	checksumsPath := filepath.Join(distDir, "checksums.txt")
	f, err := os.Open(checksumsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", checksumsPath, err)
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", checksumsPath, err)
	}

	var artifacts []release.Artifact
	for lineNum, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// goreleaser writes "<hex-sha256>  <filename>" (two spaces).
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%s:%d malformed (want '<sha256>  <filename>'): %q", checksumsPath, lineNum+1, line)
		}
		sum, filename := parts[0], parts[1]
		if len(sum) != hex.EncodedLen(sha256.Size) {
			return nil, fmt.Errorf("%s:%d sha256 length %d, want %d: %q", checksumsPath, lineNum+1, len(sum), hex.EncodedLen(sha256.Size), sum)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("%s:%d sha256 not hex: %w", checksumsPath, lineNum+1, err)
		}
		platform, ok := platformFromFilename(filename, ver)
		if !ok {
			// Not a per-platform archive (e.g., the SHA file itself, the
			// manifest we're about to write, source archives). Skip silently.
			continue
		}
		artifacts = append(artifacts, release.Artifact{
			Platform: platform,
			URL:      fmt.Sprintf("%s/%s/%s", baseURL, ver, filename),
			SHA256:   sum,
		})
	}

	// Deterministic order for byte-stable manifest output.
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Platform < artifacts[j].Platform })

	if len(artifacts) == 0 {
		return nil, fmt.Errorf("no per-platform artifacts found in %s", checksumsPath)
	}
	return artifacts, nil
}

// platformFromFilename extracts "<goos>/<goarch>" from a goreleaser archive
// name like "lit_v0.1.0_darwin_arm64.tar.gz".
//
// [LAW:types-are-the-program] The accept-shape mirrors the producer exactly:
// `lit_<version>_<goos>_<goarch>.<ext>`. Anything else returns ok=false so the
// caller can skip non-archive entries (source tarballs, the SHA file itself).
// We do NOT accept variants like "lit-darwin-arm64.tar.gz" — the producer is
// goreleaser and writes underscores; if that ever changes, this function and
// the .goreleaser.yml template change together, not behind each other.
func platformFromFilename(name, ver string) (string, bool) {
	base := filepath.Base(name)
	// Strip known archive extensions before splitting on '_'.
	for _, ext := range []string{".tar.gz", ".zip"} {
		if strings.HasSuffix(base, ext) {
			base = strings.TrimSuffix(base, ext)
			break
		}
	}
	parts := strings.Split(base, "_")
	// Expect [prefix, version, goos, goarch] — exactly four pieces.
	if len(parts) != 4 {
		return "", false
	}
	if parts[1] != ver {
		return "", false
	}
	goos, goarch := parts[2], parts[3]
	if goos == "" || goarch == "" {
		return "", false
	}
	return goos + "/" + goarch, true
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mkmanifest: "+format+"\n", args...)
	os.Exit(1)
}
