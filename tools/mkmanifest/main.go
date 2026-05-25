// mkmanifest emits the per-release manifest published alongside the binary
// artifacts. It is invoked by .github/workflows/release.yml and
// .github/workflows/release-validate.yml AFTER goreleaser has built the
// per-platform archives and written dist/checksums.txt. The workflow then
// runs `gh release create ... ./dist/release-manifest.json` to upload it as
// an asset.
//
// (Earlier iterations had this as a goreleaser pre-release hook, but
// goreleaser v2 has no valid hook point between "checksums exist" and
// "release is published", so the workflow owns ordering.)
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
//	  -version 0.1.0 \
//	  -tag v0.1.0 \
//	  -commit abcdef0 \
//	  -date 2026-05-24T15:21:00Z \
//	  -dist ./dist \
//	  -base-url https://github.com/owner/repo/releases/download \
//	  -out ./dist/release-manifest.json
//
// `-version` and `-tag` are deliberately separate flags — they encode TWO
// distinct concepts that happen to be derivable from the same git tag:
//
//   - `-version` is goreleaser's `.Version` template: the tag with the
//     leading "v" STRIPPED. platformFromFilename matches this exact segment
//     against goreleaser's archive names (which use the stripped form),
//     and it's the value stamped into the binary's `lit version`.
//   - `-tag` is the git tag itself, v-prefixed. It becomes the URL path
//     segment because that's how `gh release create "<tag>"` publishes
//     assets (`releases/download/<tag>/<filename>`). Passing `-version` here
//     instead would generate 404 URLs in the manifest.
//
// [LAW:one-source-of-truth] each parameter encodes exactly one concept;
// the release workflow extracts both from dist/metadata.json so they
// trace to a single producer.
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
		ver     = flag.String("version", "", "release version, goreleaser .Version form — v-stripped (e.g. 0.1.0); required")
		tag     = flag.String("tag", "", "git release tag, v-prefixed (e.g. v0.1.0); becomes the URL path segment under base-url; required")
		commit  = flag.String("commit", "", "git short SHA of the release commit; required")
		date    = flag.String("date", "", "RFC3339 build timestamp; required")
		distDir = flag.String("dist", "dist", "goreleaser dist directory")
		baseURL = flag.String("base-url", "", "release asset download URL prefix (no trailing slash); required")
		outPath = flag.String("out", "", "output path for release-manifest.json; required")
	)
	flag.Parse()

	// Fixed-order slice (not a map) — map iteration is randomized, so a
	// map-based check would report a different "first missing flag" across
	// runs when several are missing, making the failure non-reproducible.
	// [LAW:dataflow-not-control-flow] the ordering of the diagnostic is data
	// (this slice), not whichever key Go's runtime picked first.
	required := []struct{ name, val string }{
		{"-version", *ver},
		{"-tag", *tag},
		{"-commit", *commit},
		{"-date", *date},
		{"-base-url", *baseURL},
		{"-out", *outPath},
	}
	for _, r := range required {
		if strings.TrimSpace(r.val) == "" {
			die("required flag %s missing", r.name)
		}
	}

	max, err := migrations.MaxVersion()
	if err != nil {
		die("read migration registry: %v", err)
	}

	artifacts, err := collectArtifacts(*distDir, strings.TrimRight(*baseURL, "/"), *ver, *tag)
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
// archives named like "lit_0.1.0_darwin_arm64.tar.gz" (the version segment
// has NO leading v — see platformFromFilename for why); we strip the version
// and extract the GOOS_GOARCH segment.
//
// `ver` is used ONLY for filename matching (stripped form). `tag` is used
// ONLY for URL construction (v-prefixed, the segment `gh release create`
// publishes under). Conflating them produces 404 URLs.
func collectArtifacts(distDir, baseURL, ver, tag string) ([]release.Artifact, error) {
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
		// Verify the archive is actually present in dist/. checksums.txt
		// is the producer's claim about what exists; this turns the claim
		// into a checked fact before we promise a URL to it. Catches stale
		// checksums (an aborted goreleaser run that wrote checksums.txt but
		// failed to produce one of the archives) before they ship as 404s.
		// [LAW:enumeration-gap] accept-shape now requires "filename matches
		// pattern AND file exists", not just "filename matches pattern".
		artifactPath := filepath.Join(distDir, filename)
		if _, err := os.Stat(artifactPath); err != nil {
			return nil, fmt.Errorf("%s:%d references archive %q but %s: %w", checksumsPath, lineNum+1, filename, artifactPath, err)
		}
		artifacts = append(artifacts, release.Artifact{
			Platform: platform,
			// URL path segment is the git tag (v-prefixed), NOT the
			// stripped version — that's how `gh release create "<tag>"`
			// publishes assets. Using `ver` here would 404.
			URL:    fmt.Sprintf("%s/%s/%s", baseURL, tag, filename),
			SHA256: sum,
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
// name like "lit_0.1.0_darwin_arm64.tar.gz".
//
// Version segment naming note: goreleaser's `.Version` template strips the
// leading "v" from a tag (vX.Y.Z -> X.Y.Z), so the archive's version segment
// has NO leading v. The release.yml / release-validate.yml workflows read the
// version from dist/metadata.json (which contains the same stripped value)
// and pass that as -version to this tool, so production matching is exact.
// Test fixtures use the stripped form ("0.1.0", not "v0.1.0") to match.
//
// [LAW:types-are-the-program] The accept-shape mirrors the producer exactly:
// `lit_<version>_<goos>_<goarch>.<ext>` with a recognized archive extension
// (.tar.gz or .zip) and the literal `lit` ProjectName prefix. Anything else
// returns ok=false so the caller skips non-archive entries (source tarballs,
// the SHA file itself, the manifest we're about to write, an unrelated
// hypothetical "extra-tool_v0.1.0_linux_amd64.tar.gz" — all rejected).
//
// We do NOT accept variants like "lit-darwin-arm64.tar.gz" — the producer is
// goreleaser and writes underscores; if that ever changes, this function and
// the .goreleaser.yml template change together, not behind each other.
const projectPrefix = "lit"

func platformFromFilename(name, ver string) (string, bool) {
	base := filepath.Base(name)
	// Require a known archive extension. Files without one (checksums.txt,
	// release-manifest.json, etc.) are skipped silently.
	var stripped string
	switch {
	case strings.HasSuffix(base, ".tar.gz"):
		stripped = strings.TrimSuffix(base, ".tar.gz")
	case strings.HasSuffix(base, ".zip"):
		stripped = strings.TrimSuffix(base, ".zip")
	default:
		return "", false
	}
	parts := strings.Split(stripped, "_")
	// Expect [project, version, goos, goarch] — exactly four pieces.
	if len(parts) != 4 {
		return "", false
	}
	// Require the literal project prefix; rejects any other project's
	// archive that happened to land in dist/ alongside ours.
	if parts[0] != projectPrefix {
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
