package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Resolver translates a release tag + platform into the Target the installer
// will consume.
//
// [LAW:single-enforcer] Resolver owns "find the manifest, parse it, pick the
// platform artifact." The downgrade CLI never composes manifest URLs itself
// or fishes a specific artifact out of a Manifest.
type Resolver interface {
	Resolve(ctx context.Context, tag, platform string) (*Target, error)
}

// DefaultBaseURL is the GitHub Release download root for published lit
// artifacts. mkmanifest writes per-platform URLs under <base>/<tag>/<filename>
// and publishes release-manifest.json under <base>/<tag>/release-manifest.json,
// so the consumer fetches the manifest from the same base.
//
// [LAW:one-source-of-truth] Same value as scripts/install.sh's
// REPO_DOWNLOAD_BASE. If the repo moves, both move together.
const DefaultBaseURL = "https://github.com/brandon-fryslie/links-issue-tracker/releases/download"

// HTTPResolver is the default Resolver. It HTTP-GETs the manifest at
// <BaseURL>/<tag>/release-manifest.json and decodes it into a Manifest.
type HTTPResolver struct {
	BaseURL string       // empty defaults to DefaultBaseURL
	Client  *http.Client // nil defaults to http.DefaultClient
}

// Resolve fetches and parses the manifest, then selects the platform artifact.
func (r *HTTPResolver) Resolve(ctx context.Context, tag, platform string) (*Target, error) {
	// [LAW:single-enforcer] tag is interpolated directly into a URL path
	// segment. The CLI happens to validate before calling, but the resolver
	// is the boundary that owns URL safety — refuse here so any future
	// in-process caller (not just the CLI) can't smuggle path traversal,
	// fragment injection, or whitespace through the segment.
	// [LAW:types-are-the-program] mkmanifest's -tag flag enforces the same
	// accept shape; this is the consumer mirror.
	if !strings.HasPrefix(tag, "v") {
		return nil, fmt.Errorf("release: tag must be v-prefixed (got %q)", tag)
	}
	if strings.ContainsAny(tag, `/\`) || strings.Contains(tag, "..") || strings.ContainsAny(tag, " \t\r\n") {
		return nil, fmt.Errorf("release: tag %q is not a single URL path segment", tag)
	}
	base := r.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("%s/%s/release-manifest.json", strings.TrimRight(base, "/"), tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("release: fetch %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// [LAW:types-are-the-program] Manifest decoding is a trust boundary — the
	// JSON comes from the network. DisallowUnknownFields rejects schema drift
	// (a field added in a future producer without a consumer-side migration);
	// the trailing-data check rejects multi-document or junk-suffix payloads.
	// Both refuse silently-different-shape inputs by construction.
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("release: decode %s: %w", url, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("release: decode %s: unexpected trailing data after manifest", url)
	}
	artifact, err := SelectArtifact(m, platform)
	if err != nil {
		return nil, err
	}
	return &Target{Manifest: m, Artifact: artifact}, nil
}
