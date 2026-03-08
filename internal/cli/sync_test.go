package cli

import (
	"reflect"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/workspace"
)

func TestMapRemotesByNamePrefersFetchScope(t *testing.T) {
	entries := []map[string]string{
		{"name": "origin", "url": "ssh://push.example/repo.git", "scope": "push"},
		{"name": "origin", "url": "https://fetch.example/repo.git", "scope": "fetch"},
		{"name": "upstream", "url": "https://upstream.example/repo.git"},
	}

	got := mapRemotesByName(entries)
	want := map[string]string{
		"origin":   "https://fetch.example/repo.git",
		"upstream": "https://upstream.example/repo.git",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapRemotesByName() = %#v, want %#v", got, want)
	}
}

func TestMapGitRemotesByName(t *testing.T) {
	remotes := []workspace.GitRemote{
		{Name: "origin", URL: "https://github.com/a/repo.git"},
		{Name: "upstream", URL: "https://github.com/b/repo.git"},
	}
	got := mapGitRemotesByName(remotes)
	want := map[string]string{
		"origin":   "https://github.com/a/repo.git",
		"upstream": "https://github.com/b/repo.git",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapGitRemotesByName() = %#v, want %#v", got, want)
	}
}

func TestSameRemoteURLIgnoresGitPrefix(t *testing.T) {
	if !sameRemoteURL("https://github.com/a/repo.git", "git+https://github.com/a/repo.git") {
		t.Fatal("expected URL comparison to ignore git+ prefix")
	}
}
