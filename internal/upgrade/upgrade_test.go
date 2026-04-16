package upgrade

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeVersionTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "0.1.1", want: "v0.1.1"},
		{input: "v0.1.1", want: "v0.1.1"},
		{input: "v0.1.1-rc.1", want: "v0.1.1-rc.1"},
	}
	for _, test := range tests {
		got, err := NormalizeVersionTag(test.input)
		if err != nil {
			t.Fatalf("NormalizeVersionTag(%q) error = %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("NormalizeVersionTag(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if _, err := NormalizeVersionTag("main"); err == nil {
		t.Fatal("expected invalid version to fail")
	}
}

func TestDownloadURL(t *testing.T) {
	url, err := DownloadURL("hulz413/fox-gateway", "fox-gateway", "", "darwin", "amd64")
	if err != nil {
		t.Fatalf("DownloadURL latest error = %v", err)
	}
	if want := "https://github.com/hulz413/fox-gateway/releases/latest/download/fox-gateway_darwin_amd64"; url != want {
		t.Fatalf("latest url = %q, want %q", url, want)
	}

	url, err = DownloadURL("hulz413/fox-gateway", "fox-gateway", "v0.1.1", "linux", "amd64")
	if err != nil {
		t.Fatalf("DownloadURL pinned error = %v", err)
	}
	if want := "https://github.com/hulz413/fox-gateway/releases/download/v0.1.1/fox-gateway_linux_amd64"; url != want {
		t.Fatalf("pinned url = %q, want %q", url, want)
	}
}

func TestDownloadAndReplace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new-binary"))
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "fox-gateway")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	var progress bytes.Buffer
	if err := DownloadAndReplace(context.Background(), nil, server.URL, target, &progress); err != nil {
		t.Fatalf("DownloadAndReplace error = %v", err)
	}
	if !strings.Contains(progress.String(), "Downloading...") {
		t.Fatalf("progress output = %q, expected download progress", progress.String())
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(body) != "new-binary" {
		t.Fatalf("target contents = %q, want %q", string(body), "new-binary")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat error = %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("target mode = %v, expected executable", info.Mode().Perm())
	}
}

func TestTargetSuffixRejectsUnsupported(t *testing.T) {
	_, err := TargetSuffix("linux", "arm64")
	if err == nil || !strings.Contains(err.Error(), "unsupported Linux architecture") {
		t.Fatalf("unexpected error = %v", err)
	}
}
