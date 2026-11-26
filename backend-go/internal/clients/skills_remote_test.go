package clients

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFindRemoteSkillFileSupportsRootAndSingleSkillLayouts(t *testing.T) {
	tests := []struct {
		name         string
		tree         []githubTreeEntry
		requested    string
		wantFile     string
		wantRoot     string
		wantRelative []string
	}{
		{
			name: "仓库根目录",
			tree: []githubTreeEntry{
				{Path: "SKILL.md", Type: "blob"},
				{Path: "scripts/delegate.py", Type: "blob"},
				{Path: "references/runners.md", Type: "blob"},
				{Path: "README.md", Type: "blob"},
				{Path: "assets", Type: "tree"},
			},
			requested:    "oil-delegate",
			wantFile:     "SKILL.md",
			wantRoot:     ".",
			wantRelative: []string{"SKILL.md", "scripts/delegate.py", "references/runners.md", "README.md"},
		},
		{
			name: "任意单技能目录",
			tree: []githubTreeEntry{
				{Path: "skill/SKILL.md", Type: "blob"},
				{Path: "skill/scripts/run.sh", Type: "blob"},
				{Path: "README.md", Type: "blob"},
			},
			requested:    "opentui",
			wantFile:     "skill/SKILL.md",
			wantRoot:     "skill",
			wantRelative: []string{"SKILL.md", "scripts/run.sh"},
		},
		{
			name: "skills 目录按 slug 命中",
			tree: []githubTreeEntry{
				{Path: "SKILL.md", Type: "blob"},
				{Path: "skills/foo/SKILL.md", Type: "blob"},
				{Path: "skills/foo/references/a.md", Type: "blob"},
				{Path: "skills/bar/SKILL.md", Type: "blob"},
				{Path: "skills/bar/scripts/b.sh", Type: "blob"},
			},
			requested:    "foo",
			wantFile:     "skills/foo/SKILL.md",
			wantRoot:     "skills/foo",
			wantRelative: []string{"SKILL.md", "references/a.md"},
		},
		{
			name: "嵌套目录按后缀命中",
			tree: []githubTreeEntry{
				{Path: "plugins/foo/skills/bar/SKILL.md", Type: "blob"},
				{Path: "plugins/foo/skills/bar/scripts/x.sh", Type: "blob"},
				{Path: "plugins/foo/skills/other/SKILL.md", Type: "blob"},
			},
			requested:    "bar",
			wantFile:     "plugins/foo/skills/bar/SKILL.md",
			wantRoot:     "plugins/foo/skills/bar",
			wantRelative: []string{"SKILL.md", "scripts/x.sh"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skillFile, skillRoot, err := findRemoteSkillFile(test.tree, test.requested)
			require.NoError(t, err)
			require.Equal(t, test.wantFile, skillFile)
			require.Equal(t, test.wantRoot, skillRoot)

			files, err := collectRemoteSkillFiles(test.tree, skillFile, skillRoot)
			require.NoError(t, err)
			require.ElementsMatch(t, test.wantRelative, remoteSkillRelativePaths(files))
		})
	}
}

func TestFindRemoteSkillFileRejectsAmbiguousSkillLayout(t *testing.T) {
	tree := []githubTreeEntry{
		{Path: "SKILL.md", Type: "blob"},
		{Path: "other/SKILL.md", Type: "blob"},
	}
	_, _, err := findRemoteSkillFile(tree, "missing")
	require.ErrorContains(t, err, "存在多个 SKILL.md")
}

func TestFindRemoteSkillFileKeepsMissingSkillError(t *testing.T) {
	_, _, err := findRemoteSkillFile([]githubTreeEntry{{Path: "README.md", Type: "blob"}}, "foo")
	require.EqualError(t, err, "GitHub 仓库中找不到 foo/SKILL.md")
}

func TestCollectRemoteSkillFilesRejectsInvalidPath(t *testing.T) {
	tree := []githubTreeEntry{
		{Path: "SKILL.md", Type: "blob"},
		{Path: "scripts/../escape.md", Type: "blob"},
	}
	_, err := collectRemoteSkillFiles(tree, "SKILL.md", ".")
	require.ErrorContains(t, err, "无效文件路径")
}

func remoteSkillRelativePaths(files []remoteSkillFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.relative)
	}
	return paths
}

func TestExtractRemoteSkillArchiveFiltersAndStripsRoot(t *testing.T) {
	archive := buildRemoteSkillArchive(t, []testArchiveEntry{
		{name: "oil-delegate-abc123/SKILL.md", content: []byte("---\nname: oil-delegate\ndescription: 示例\n---\n")},
		{name: "oil-delegate-abc123/scripts/run.sh", content: []byte("echo hi")},
		{name: "oil-delegate-abc123/assets/diagram.svg", content: []byte("<svg/>")},
		{name: "oil-delegate-abc123/README.md", content: []byte("不安装")},
		{name: "oil-delegate-abc123/.gitignore", content: []byte("*.log")},
	})
	wanted := map[string]string{
		"SKILL.md":           "SKILL.md",
		"scripts/run.sh":     "scripts/run.sh",
		"assets/diagram.svg": "assets/diagram.svg",
	}

	files, err := extractRemoteSkillArchive(bytes.NewReader(archive), wanted)
	require.NoError(t, err)
	require.Len(t, files, 3)
	require.Equal(t, "echo hi", string(files["scripts/run.sh"]))
	require.Equal(t, "<svg/>", string(files["assets/diagram.svg"]))
	require.NotContains(t, files, "README.md")
	require.NotContains(t, files, ".gitignore")
}

func TestExtractRemoteSkillArchiveRejectsSymlink(t *testing.T) {
	archive := buildRemoteSkillArchive(t, []testArchiveEntry{
		{name: "repo-sha/SKILL.md", content: []byte("---\nname: demo\ndescription: 示例\n---\n")},
		{name: "repo-sha/current.md", link: "references/v2.md"},
	})
	wanted := map[string]string{"SKILL.md": "SKILL.md", "current.md": "current.md"}

	_, err := extractRemoteSkillArchive(bytes.NewReader(archive), wanted)
	require.ErrorContains(t, err, "不支持符号链接")
}

func TestExtractRemoteSkillArchiveRejectsMissingFile(t *testing.T) {
	archive := buildRemoteSkillArchive(t, []testArchiveEntry{
		{name: "repo-sha/SKILL.md", content: []byte("---\nname: demo\ndescription: 示例\n---\n")},
	})
	_, err := extractRemoteSkillArchive(bytes.NewReader(archive), map[string]string{
		"SKILL.md":     "SKILL.md",
		"references/a": "references/a",
	})
	require.ErrorContains(t, err, "归档缺少文件")
}

func TestDownloadRemoteSkillArchiveRetriesTransientFailure(t *testing.T) {
	archive := buildRemoteSkillArchive(t, []testArchiveEntry{
		{name: "repo-sha/SKILL.md", content: []byte("---\nname: demo\ndescription: 示例\n---\n")},
	})
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "temporary", http.StatusBadGateway)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	restore := remoteSkillRetryBaseDelay
	remoteSkillRetryBaseDelay = time.Millisecond
	defer func() { remoteSkillRetryBaseDelay = restore }()

	files, err := downloadRemoteSkillArchive(context.Background(), server.URL, map[string]string{"SKILL.md": "SKILL.md"})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)
	require.Contains(t, files, "SKILL.md")
}

func TestDownloadRemoteSkillArchiveDoesNotRetryNotFound(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := downloadRemoteSkillArchive(context.Background(), server.URL, map[string]string{"SKILL.md": "SKILL.md"})
	require.ErrorContains(t, err, "HTTP 404")
	require.Equal(t, 1, attempts)
}

func TestFetchJSONWithRetryRecoversFromServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, `{"message":"temporary"}`, http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"query":"ui","skills":[]}`))
	}))
	defer server.Close()

	restore := remoteSkillRetryBaseDelay
	remoteSkillRetryBaseDelay = time.Millisecond
	defer func() { remoteSkillRetryBaseDelay = restore }()

	var payload skillSearchAPIResponse
	err := fetchJSONWithRetry(context.Background(), server.URL, maxSkillSearchResponse, &payload, time.Second, 2)
	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, "ui", payload.Query)
}

func TestFetchJSONWithRetryDoesNotRetryNotFound(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	var payload skillSearchAPIResponse
	err := fetchJSONWithRetry(context.Background(), server.URL, maxSkillSearchResponse, &payload, time.Second, 2)
	require.ErrorContains(t, err, "HTTP 404: Not Found")
	require.Equal(t, 1, attempts)
}

func TestFetchJSONWithRetryReportsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	var payload skillSearchAPIResponse
	err := fetchJSONWithRetry(context.Background(), server.URL, maxSkillSearchResponse, &payload, 20*time.Millisecond, 2)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, skillsSearchErrorMessage(err), "响应超时")
}

type testArchiveEntry struct {
	name    string
	content []byte
	link    string
}

func buildRemoteSkillArchive(t *testing.T, entries []testArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(entry.content))}
		if entry.link != "" {
			header.Typeflag = tar.TypeSymlink
			header.Size = 0
			header.Linkname = entry.link
		}
		require.NoError(t, tarWriter.WriteHeader(header))
		if entry.link == "" {
			_, err := tarWriter.Write(entry.content)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	return buffer.Bytes()
}
