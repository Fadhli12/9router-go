package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.1", "1.0.0", 1},
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.8.6-rc1", "1.8.5", 1},
		{"1.8.5", "1.8.6", -1},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestGetCachedInfo(t *testing.T) {
	info := GetCachedInfo()
	if info == nil {
		t.Fatal("expected non-nil UpdateInfo")
	}
	if info.CurrentVersion == "" {
		t.Error("expected non-empty CurrentVersion")
	}
}

func TestCheckUpdate_Manifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := map[string]any{
			"latestVersion": "2.0.0",
			"downloadUrl":   "https://example.com/downloads/9router-go",
			"releaseNotes":  "Major release 2.0.0",
			"sha256":        "abcdef123456",
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, manifest)
	}))
	defer server.Close()

	os.Setenv("UPDATE_URL", server.URL)
	defer os.Unsetenv("UPDATE_URL")

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}

	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true for version 2.0.0 vs %s", CurrentVersion)
	}
	if info.LatestVersion != "2.0.0" {
		t.Errorf("expected latestVersion 2.0.0, got %s", info.LatestVersion)
	}
	if info.DownloadURL != "https://example.com/downloads/9router-go" {
		t.Errorf("expected downloadUrl, got %s", info.DownloadURL)
	}
}

func TestCheckUpdate_GitHubReleasesFallback(t *testing.T) {
	// Mock failing manifest endpoint
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer manifestServer.Close()

	os.Setenv("UPDATE_URL", manifestServer.URL)
	defer os.Unsetenv("UPDATE_URL")

	// Directly test checkGitHubReleases
	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"tag_name": "v3.0.0",
			"name":     "Release 3.0.0",
			"body":     "Awesome new features",
			"assets": []map[string]any{
				{
					"name":                 "9router-go_darwin_arm64.tar.gz",
					"browser_download_url": "https://github.com/releases/9router-go_darwin_arm64.tar.gz",
				},
				{
					"name":                 "9router-go_linux_amd64.tar.gz",
					"browser_download_url": "https://github.com/releases/9router-go_linux_amd64.tar.gz",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, resp)
	}))
	defer ghServer.Close()

	info, err := checkGitHubReleases(context.Background(), ghServer.URL)
	if err != nil {
		t.Fatalf("checkGitHubReleases failed: %v", err)
	}

	if info.LatestVersion != "3.0.0" {
		t.Errorf("expected latestVersion 3.0.0, got %s", info.LatestVersion)
	}
	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true")
	}
	if info.Source != "github_releases" {
		t.Errorf("expected source github_releases, got %s", info.Source)
	}
}

func TestExtractExecutableBytes_TarGz(t *testing.T) {
	// Create a dummy .tar.gz containing a 9router-go binary payload
	binaryContent := bytes.Repeat([]byte("BINARY_PAYLOAD_CONTENT_TEST_EXEC_DATA"), 100)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "9router-go",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	tw.Close()
	gw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "9router-go_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("extractExecutableBytes failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted content does not match expected payload")
	}
}

func TestExtractExecutableBytes_Zip(t *testing.T) {
	binaryContent := bytes.Repeat([]byte("ZIP_BINARY_PAYLOAD_TEST_DATA"), 100)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create("9router-go.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write(binaryContent); err != nil {
		t.Fatalf("write zip content: %v", err)
	}
	zw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "9router-go_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extract zip failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted zip content does not match payload")
	}
}

func TestMatchReleaseAsset(t *testing.T) {
	assets := []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{Name: "9router-go_linux_amd64.tar.gz", BrowserDownloadURL: "url-linux-amd64"},
		{Name: "9router-go_darwin_arm64.tar.gz", BrowserDownloadURL: "url-darwin-arm64"},
		{Name: "9router-go_windows_amd64.zip", BrowserDownloadURL: "url-windows-amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: "url-checksums"},
	}

	if url := matchReleaseAsset(assets, "darwin", "arm64"); url != "url-darwin-arm64" {
		t.Errorf("expected url-darwin-arm64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "linux", "amd64"); url != "url-linux-amd64" {
		t.Errorf("expected url-linux-amd64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "windows", "amd64"); url != "url-windows-amd64" {
		t.Errorf("expected url-windows-amd64, got %s", url)
	}
}

func TestAutoUpdate_StatusAndToggle(t *testing.T) {
	SetAutoUpdate(true)
	if !IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be true")
	}

	status := GetStatus()
	if !status.AutoUpdateEnabled {
		t.Errorf("expected status.AutoUpdateEnabled to be true")
	}
	if status.CurrentVersion != CurrentVersion {
		t.Errorf("expected currentVersion %s, got %s", CurrentVersion, status.CurrentVersion)
	}

	SetAutoUpdate(false)
	if IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be false")
	}
}

func TestExtractExecutableBytes_RejectsHTML(t *testing.T) {
	htmlData := []byte("<!DOCTYPE html><html><head><title>Release</title></head><body><h1>Not an executable</h1></body></html>")

	tests := []struct {
		name     string
		filename string
	}{
		{name: "raw html asset", filename: "9router-go"},
		{name: "fake tar.gz with html content", filename: "9router-go_darwin_arm64.tar.gz"},
		{name: "fake zip with html content", filename: "9router-go_windows_amd64.zip"},
		{name: "plain text file", filename: "release-notes.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted, err := extractExecutableBytes(htmlData, tt.filename)
			if err == nil {
				t.Fatalf("expected error for non-executable content, got nil")
			}
			if extracted != nil {
				t.Errorf("expected nil extracted bytes, got %d bytes", len(extracted))
			}
			if !strings.Contains(err.Error(), "magic bytes check failed") {
				t.Errorf("expected magic bytes error, got: %v", err)
			}
		})
	}
}

func TestExtractExecutableBytes_ValidBinaries(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		data     []byte
	}{
		{
			name:     "valid elf binary",
			filename: "9router-go_linux_amd64",
			data:     []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00},
		},
		{
			name:     "valid mach-o 64-bit binary",
			filename: "9router-go_darwin_arm64",
			data:     []byte{0xfe, 0xed, 0xfa, 0xcf, 0x00, 0x00, 0x00, 0x01},
		},
		{
			name:     "valid pe binary",
			filename: "9router-go_windows_amd64.exe",
			data:     []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted, err := extractExecutableBytes(tt.data, tt.filename)
			if err != nil {
				t.Fatalf("extractExecutableBytes failed: %v", err)
			}
			if !bytes.Equal(extracted, tt.data) {
				t.Errorf("extracted data does not match input binary")
			}
		})
	}
}

func TestPerformSelfUpdate_RejectsHTMLContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>GitHub Release Page</body></html>"))
	}))
	defer server.Close()

	err := PerformSelfUpdate(server.URL, "")
	if err == nil {
		t.Fatal("expected error when downloading HTML content-type, got nil")
	}

	if !strings.Contains(err.Error(), "downloaded asset returned HTML content") {
		t.Errorf("expected HTML content error, got: %v", err)
	}
}

func TestPerformSelfUpdate_RejectsHTMLBodyWithoutContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Fake Octet Stream</body></html>"))
	}))
	defer server.Close()

	err := PerformSelfUpdate(server.URL, "")
	if err == nil {
		t.Fatal("expected error when downloading non-executable binary bytes, got nil")
	}

	if !strings.Contains(err.Error(), "magic bytes check failed") {
		t.Errorf("expected magic bytes check failure, got: %v", err)
	}
}

func TestPerformSelfUpdate_RejectsReleasePageURL(t *testing.T) {
	urls := []string{
		"https://github.com/luqman-v1/9router-go/releases/latest",
		"https://github.com/luqman-v1/9router-go/releases/latest/",
		"https://github.com/luqman-v1/9router-go/releases/tag/v1.9.6",
		"https://github.com/luqman-v1/9router-go/releases",
		"https://example.com/downloads/releases/latest",
	}

	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			err := PerformSelfUpdate(u, "")
			if err == nil {
				t.Fatalf("expected error for release page URL %s, got nil", u)
			}
			if !strings.Contains(err.Error(), "release page") {
				t.Errorf("expected release page error for %s, got: %v", u, err)
			}
		})
	}
}

func TestIsReleasePageURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://github.com/luqman-v1/9router-go/releases/latest", true},
		{"https://github.com/luqman-v1/9router-go/releases/latest/", true},
		{"https://github.com/luqman-v1/9router-go/releases/tag/v1.9.6", true},
		{"https://github.com/luqman-v1/9router-go/releases", true},
		{"https://example.com/repo/releases/latest", true},
		{"https://github.com/luqman-v1/9router-go/releases/download/v1.9.6/9router-go_linux_amd64.tar.gz", false},
		{"https://objects.githubusercontent.com/github-production-release-asset-2e65be/asset.tar.gz", false},
		{"https://example.com/downloads/9router-go", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isReleasePageURL(tt.url)
			if got != tt.expected {
				t.Errorf("isReleasePageURL(%q) = %v, want %v", tt.url, got, tt.expected)
			}
		})
	}
}
