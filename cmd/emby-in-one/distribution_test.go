package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRootPath() string {
	return filepath.Clean(filepath.Join("..", ".."))
}

func existingPaths(paths ...string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func TestShellScriptsDoNotUseUTF8BOM(t *testing.T) {
	root := repoRootPath()
	candidates := existingPaths(
		filepath.Join(root, "install.sh"),
		filepath.Join(root, "emby-in-one-cli.sh"),
		filepath.Join(root, "Emby-In-One-Go", "install.sh"),
		filepath.Join(root, "Emby-In-One-Go", "emby-in-one-cli.sh"),
	)
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
			t.Fatalf("script %s starts with UTF-8 BOM", path)
		}
	}
}

func TestInstallScriptMentionsStandaloneDistributionLayout(t *testing.T) {
	root := repoRootPath()
	installPath := filepath.Join(root, "install.sh")
	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	text := string(data)
	for _, fragment := range []string{"cmd", "internal", "go.mod", "go.sum", "public"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("install.sh does not mention standalone distribution fragment %q", fragment)
		}
	}
}

func TestStandaloneDistributionDockerfilesDoNotReferenceGoBackendPrefix(t *testing.T) {
	root := repoRootPath()
	candidates := existingPaths(filepath.Join(root, "Emby-In-One-Go", "Dockerfile"))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		if _, err := os.Stat(filepath.Join(root, "cmd")); err == nil {
			if _, err := os.Stat(filepath.Join(root, "internal")); err == nil {
				candidates = append(candidates, filepath.Join(root, "Dockerfile"))
			}
		}
	}
	if len(candidates) == 0 {
		t.Skip("no standalone distribution Dockerfile found in this workspace")
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "COPY go-backend/") {
			t.Fatalf("standalone Dockerfile %s still references go-backend/ paths", path)
		}
		for _, expected := range []string{"COPY go.mod ./", "COPY go.sum ./", "COPY cmd ./cmd", "COPY internal ./internal", "COPY public ./public", "CGO_ENABLED=0"} {
			if !strings.Contains(text, expected) {
				t.Fatalf("standalone Dockerfile %s missing %q", path, expected)
			}
		}
		for _, forbidden := range []string{"COPY third_party", "build-essential", "CGO_ENABLED=1"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("standalone Dockerfile %s still contains %q", path, forbidden)
			}
		}
	}
}

func TestDockerRuntimeUsesDistrolessStatic(t *testing.T) {
	root := repoRootPath()
	cases := []struct {
		path               string
		expectedStageCount int
		optional           bool
	}{
		{path: filepath.Join(root, "Dockerfile"), expectedStageCount: 1},
		{path: filepath.Join(root, "install.sh"), expectedStageCount: 1},
		{path: filepath.Join(root, "emby-in-one-cli.sh"), expectedStageCount: 2},
		{path: filepath.Join(root, "Emby-In-One-Go", "Dockerfile"), expectedStageCount: 1, optional: true},
		{path: filepath.Join(root, "Emby-In-One-Go", "install.sh"), expectedStageCount: 1, optional: true},
		{path: filepath.Join(root, "Emby-In-One-Go", "emby-in-one-cli.sh"), expectedStageCount: 2, optional: true},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			if tc.optional && os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", tc.path, err)
		}
		text := string(data)
		if got := strings.Count(text, "FROM gcr.io/distroless/static-debian13"); got != tc.expectedStageCount {
			t.Fatalf("%s has %d distroless runtime stage(s), want %d", tc.path, got, tc.expectedStageCount)
		}
		for _, forbidden := range []string{
			"FROM debian:bookworm-slim",
			"apt-get install -y --no-install-recommends ca-certificates tzdata",
			"RUN mkdir -p /app/config /app/data /app/public",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still contains runtime-only fragment %q", tc.path, forbidden)
			}
		}
	}
}

func TestShellScriptsUseLFLineEndings(t *testing.T) {
	root := repoRootPath()
	candidates := existingPaths(
		filepath.Join(root, "install.sh"),
		filepath.Join(root, "emby-in-one-cli.sh"),
		filepath.Join(root, "Emby-In-One-Go", "install.sh"),
		filepath.Join(root, "Emby-In-One-Go", "emby-in-one-cli.sh"),
	)
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "\r") {
			t.Fatalf("script %s contains CRLF line endings", path)
		}
	}
}

func TestInstallScriptsHaveSingleValidScriptDirAssignment(t *testing.T) {
	root := repoRootPath()
	candidates := existingPaths(
		filepath.Join(root, "install.sh"),
		filepath.Join(root, "Emby-In-One-Go", "install.sh"),
	)
	const want = `SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"`
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Count(text, want) != 1 {
			t.Fatalf("install script %s should contain exactly one valid SCRIPT_DIR assignment", path)
		}
		for _, bad := range []string{
			`SCRIPT_DIR="$(cd "$(dirname "# ── 5. 复制/下载项目文件 ──`,
			`# ── 6. 创建数据目录 ──")" && pwd)"`,
		} {
			if strings.Contains(text, bad) {
				t.Fatalf("install script %s contains corrupted fragment %q", path, bad)
			}
		}
	}
}

func TestAdminHTMLSaveServerHandlesUpstreamErrors(t *testing.T) {
	root := repoRootPath()
	// After Phase 3 refactoring, JS was extracted to admin.js; check both files.
	candidates := existingPaths(
		filepath.Join(root, "public", "admin.html"),
		filepath.Join(root, "public", "admin.js"),
		filepath.Join(root, "Emby-In-One-Go", "public", "admin.html"),
		filepath.Join(root, "Emby-In-One-Go", "public", "admin.js"),
	)
	var combined string
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		combined += string(data) + "\n"
	}
	for _, fragment := range []string{
		"async saveServer()",
		"const res = await this.api(",
		"'/admin/api/upstream'",
		"res.warning",
		"res.warning);",
		"await this.refreshServers();",
		"catch (e)",
		"e.message",
	} {
		if !strings.Contains(combined, fragment) {
			t.Fatalf("admin.html+admin.js is missing %q", fragment)
		}
	}
}
