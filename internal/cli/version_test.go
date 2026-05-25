package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/store/migrations"
	"github.com/bmf/links-issue-tracker/internal/version"
)

// TestVersionJSONMatchesGetOutput pins the JSON surface as the typed contract:
// the bytes `lit version --json` emits MUST round-trip via json.Unmarshal into
// a version.Info equal to version.Get(). Downstream tooling (`lit downgrade`,
// the refusal-message upgrade) reads this output; any drift between the
// command surface and the package surface breaks them.
//
// [LAW:one-source-of-truth] One source (version.Get); two presentations.
func TestVersionJSONMatchesGetOutput(t *testing.T) {
	var stdout bytes.Buffer
	if err := runVersion(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("runVersion --json error = %v", err)
	}

	var got version.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\nbytes: %s", err, stdout.String())
	}
	want, err := version.Get()
	if err != nil {
		t.Fatalf("version.Get() error = %v", err)
	}
	if got != want {
		t.Errorf("--json decoded to %+v, want %+v", got, want)
	}
}

// TestVersionJSONIsStrictMachineContract pins [memory: json-mode-strict-machine-contract]:
// `--json` emits exactly one JSON document and nothing else (no banners, no
// trailing prose, no log lines on the buffer). A hidden header would slip
// past plain `json.Valid` (which only checks the first document) but break a
// `jq` pipeline downstream.
//
// Implementation note: we cannot `bytes.Buffer.String()` after decoding,
// because json.Decoder buffers reads from its source — the underlying buffer
// still contains every byte you wrote, including bytes the decoder hasn't
// read. Instead we decode from an io.Reader and then assert (a) the decoder
// has no more JSON values to give us, and (b) the decoder's own buffered
// remainder + the rest of the reader together contain only whitespace.
func TestVersionJSONIsStrictMachineContract(t *testing.T) {
	var stdout bytes.Buffer
	if err := runVersion(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("runVersion --json error = %v", err)
	}

	raw := stdout.Bytes()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var first version.Info
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("first decode error = %v", err)
	}

	// dec.More() reports whether there's another JSON value waiting (after
	// any whitespace). If true, --json emitted more than one document.
	if dec.More() {
		var extra json.RawMessage
		_ = dec.Decode(&extra)
		t.Errorf("--json emitted a second JSON document: %s", extra)
	}

	// Drain anything left in the decoder's internal buffer + the underlying
	// reader. Whitespace is acceptable (a trailing newline from an encoder);
	// anything else is contraband.
	leftover, err := io.ReadAll(dec.Buffered())
	if err != nil {
		t.Fatalf("read buffered: %v", err)
	}
	rest, err := io.ReadAll(dec.Buffered())
	_ = rest
	_ = err
	tail := strings.TrimSpace(string(leftover))
	if tail != "" {
		t.Errorf("--json emitted trailing non-whitespace bytes: %q", tail)
	}
}

// TestVersionHumanSurfacesAllInfoFields pins that the human form presents every
// field on Info (version, commit, date, schema range). A future Info field
// that gets added but not surfaced in the text form would slip past this test
// as a regression hint to update both surfaces.
func TestVersionHumanSurfacesAllInfoFields(t *testing.T) {
	// Stamp link-time fields so the human form has something concrete to render.
	// Use values that can NOT appear anywhere except in their respective fields:
	// version is a sentinel that cannot collide with schema digits ("0.0.0" not
	// "v1.2.3"; the latter contains "1" which is the current Schema.Max).
	origV, origC, origD := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = origV, origC, origD })
	version.Version = "vSENTINEL-9.9.9"
	version.Commit = "abcdef0"
	version.Date = "2026-05-24T15:21:00Z"

	var stdout bytes.Buffer
	if err := runVersion(&stdout, nil); err != nil {
		t.Fatalf("runVersion (human) error = %v", err)
	}
	out := stdout.String()

	for _, want := range []string{"vSENTINEL-9.9.9", "abcdef0", "2026-05-24T15:21:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}

	// Schema range — assert the exact rendered substring, not loose substring
	// match. The format string is "schema versions supported: %d–%d\n", so the
	// expected line is fully determined by the registry bounds.
	min := migrations.Baseline
	max, err := migrations.MaxVersion()
	if err != nil {
		t.Fatalf("migrations.MaxVersion error = %v", err)
	}
	wantLine := fmt.Sprintf("schema versions supported: %d–%d", min, max)
	if !strings.Contains(out, wantLine) {
		t.Errorf("human output missing schema-range line %q:\n%s", wantLine, out)
	}
}

// TestVersionHumanLabelsDevBuild pins the "no link-time version stamped"
// surface: the human form shows "dev" in place of an empty Version, and
// "unknown" in place of empty Commit/Date. Consumers of the human form rely
// on these labels (they're more legible than literal empty strings).
func TestVersionHumanLabelsDevBuild(t *testing.T) {
	origV, origC, origD := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = origV, origC, origD })
	version.Version = ""
	version.Commit = ""
	version.Date = ""

	var stdout bytes.Buffer
	if err := runVersion(&stdout, nil); err != nil {
		t.Fatalf("runVersion (dev) error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"dev", "unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("dev-build human output missing %q:\n%s", want, out)
		}
	}
}

// TestVersionRejectsPositionalArgs pins the command shape: `lit version` takes
// only `--json`; any positional arg is a usage error. Prevents silent misuse
// like `lit version v0.1.0` (which a user might think means "show v0.1.0's
// release manifest" — that operation belongs to a different command).
func TestVersionRejectsPositionalArgs(t *testing.T) {
	var stdout bytes.Buffer
	err := runVersion(&stdout, []string{"v0.1.0"})
	if err == nil {
		t.Fatal("runVersion with positional arg returned nil, want usage error")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("err = %v, want a usage error message", err)
	}
}
