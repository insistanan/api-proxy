package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"insistanan/api-proxy/internal/clients/filetxn"
)

const (
	manifestFileName = "skills-manifest.json"
	manifestVersion  = 1
)

// ManagedAgentKeys 列出所有受项目 Skill 清单托管的 Agent 目录 key。
var ManagedAgentKeys = []string{"agents", "claude-code", "codex", "opencode", "pi", "cursor"}

// IsManagedAgentKey 判断 key 是否属于可由项目目录自动同步的 Agent 目录。
func IsManagedAgentKey(key string) bool {
	return slices.Contains(ManagedAgentKeys, key)
}

// Manifest 记录项目目录中每个受管 Skill 在各 Agent 上的启用状态（Git 可追踪）。
type Manifest struct {
	raw     []byte
	Version int                 `json:"version"`
	Skills  map[string][]string `json:"skills"`
}

// SyncReport 汇总一次 manifest -> Agent 目录同步的结果。
type SyncReport struct {
	Synced  int      `json:"synced"`
	Removed int      `json:"removed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// ManifestPath 返回项目目录中 skills-manifest.json 的路径。
func ManifestPath(projectDir string) (string, error) {
	root, err := resolveProjectDir(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, manifestFileName), nil
}

// ReadManifest 读取并规范化 skills-manifest.json；文件不存在时返回空清单。
func ReadManifest(projectDir string) (Manifest, error) {
	path, err := ManifestPath(projectDir)
	if err != nil {
		return Manifest{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{Version: manifestVersion, Skills: map[string][]string{}}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("读取 Skill 启用清单失败: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 Skill 启用清单失败: %w", err)
	}
	if manifest.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("不支持的 Skill 启用清单版本: %d", manifest.Version)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	manifest.raw = raw
	return normalizeManifest(manifest), nil
}

// WriteManifest 原子写入规范化后的 skills-manifest.json，保持键和数组有序以便 Git 追踪。
func WriteManifest(projectDir string, manifest Manifest) error {
	path, err := ManifestPath(projectDir)
	if err != nil {
		return err
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	normalized := normalizeManifest(manifest)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Skill 启用清单失败: %w", err)
	}
	data = append(data, '\n')
	if err := filetxn.Replace(path, manifest.raw, data, "", 0600); err != nil {
		return fmt.Errorf("保存 Skill 启用清单失败: %w", err)
	}
	return nil
}

func normalizeManifest(manifest Manifest) Manifest {
	normalized := Manifest{
		raw:     manifest.raw,
		Version: manifestVersion,
		Skills:  make(map[string][]string, len(manifest.Skills)),
	}
	for skillName, agents := range manifest.Skills {
		normalized.Skills[skillName] = normalizeAgentList(agents)
	}
	return normalized
}

func normalizeAgentList(agents []string) []string {
	seen := make(map[string]struct{}, len(agents))
	result := make([]string, 0, len(agents))
	for _, agent := range agents {
		if _, ok := seen[agent]; ok {
			continue
		}
		seen[agent] = struct{}{}
		result = append(result, agent)
	}
	sort.Strings(result)
	return result
}

// RecordSkillTargets 在安装、导入、归集或复制后更新 manifest 中的托管记录。
// mergeExisting 为 true 时保留已有启用 Agent 并合并新目标中的受管 Agent。
func RecordSkillTargets(projectDir, skillName string, targets []string, mergeExisting bool) error {
	if !ValidName(skillName) {
		return errors.New("Skill 名称必须为小写字母、数字和连字符")
	}
	manifest, err := ReadManifest(projectDir)
	if err != nil {
		return err
	}
	nextAgents := make([]string, 0, len(targets))
	if mergeExisting {
		nextAgents = append(nextAgents, manifest.Skills[skillName]...)
	}
	for _, target := range targets {
		if IsManagedAgentKey(target) {
			nextAgents = append(nextAgents, target)
		}
	}
	manifest.Skills[skillName] = normalizeAgentList(nextAgents)
	return WriteManifest(projectDir, manifest)
}

func collectSkillFileHashes(root string, skipBackups bool) (map[string][32]byte, error) {
	hashes := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipBackups && current != root && (entry.Name() == backupsDirName || backupDirPattern.MatchString(entry.Name())) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("不支持比较符号链接: %s", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持特殊文件: %s", current)
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		sum, err := hashFileSHA256(current)
		if err != nil {
			return err
		}
		// 执行位也是副本契约的一部分；普通读写权限不参与内容判断。
		hashes[filepath.ToSlash(rel)] = sha256.Sum256(append(sum[:], byte(info.Mode().Perm()&0111)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hashes, nil
}

func hashFileSHA256(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], hasher.Sum(nil))
	return sum, nil
}
