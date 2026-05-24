package main

import "testing"

// TestPlatformFromFilenameAcceptReject is the accept/reject table for
// platformFromFilename. The producer (goreleaser, configured by
// .goreleaser.yml) writes archives as `lit_<version>_<goos>_<goarch>.<ext>`.
// This table mirrors that producer shape exactly: any other shape returns
// ok=false so the caller skips non-archive entries (source tarballs, the SHA
// file, the manifest itself) silently.
//
// [LAW:types-are-the-program] The producer is the source of truth; the parser
// rejects every name that does not match the produced shape. If goreleaser's
// archive template changes, .goreleaser.yml and this table change together.
func TestPlatformFromFilenameAcceptReject(t *testing.T) {
	cases := []struct {
		name string
		ver  string
		want string
		ok   bool
	}{
		{"lit_v0.1.0_darwin_arm64.tar.gz", "v0.1.0", "darwin/arm64", true},
		{"lit_v0.1.0_darwin_amd64.tar.gz", "v0.1.0", "darwin/amd64", true},
		{"lit_v0.1.0_linux_amd64.tar.gz", "v0.1.0", "linux/amd64", true},
		{"lit_v0.1.0_linux_arm64.tar.gz", "v0.1.0", "linux/arm64", true},
		{"lit_v0.1.0_windows_amd64.zip", "v0.1.0", "windows/amd64", true},

		// Reject: wrong version (someone built two tags into one dist somehow).
		{"lit_v0.2.0_darwin_arm64.tar.gz", "v0.1.0", "", false},
		// Reject: non-archive entries goreleaser might emit (source tarball,
		// SHA file, the manifest itself).
		{"lit_v0.1.0_source.tar.gz", "v0.1.0", "", false},
		{"checksums.txt", "v0.1.0", "", false},
		{"release-manifest.json", "v0.1.0", "", false},
		// Reject: legacy dash-style naming we explicitly do not accept (must
		// fail loudly so a producer change is observed, not silently ignored).
		{"lit-v0.1.0-darwin-arm64.tar.gz", "v0.1.0", "", false},
		// Reject: too few parts.
		{"lit_v0.1.0.tar.gz", "v0.1.0", "", false},
		// Reject: empty components.
		{"lit_v0.1.0__arm64.tar.gz", "v0.1.0", "", false},
	}
	for _, tc := range cases {
		got, ok := platformFromFilename(tc.name, tc.ver)
		if ok != tc.ok {
			t.Errorf("platformFromFilename(%q, %q) ok = %v, want %v", tc.name, tc.ver, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("platformFromFilename(%q, %q) = %q, want %q", tc.name, tc.ver, got, tc.want)
		}
	}
}
