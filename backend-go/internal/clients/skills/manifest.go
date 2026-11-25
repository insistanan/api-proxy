package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"insistanan/api-proxy/internal/storage/atomicfile"
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
	if len(raw) == 0 {
		return Manifest{Version: manifestVersion, Skills: map[string][]string{}}, nil
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 Skill 启用清单失败: %w", err)
	}
	if manifest.Version != 0 && manifest.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("不支持的 Skill 启用清单版本: %d", manifest.Version)
	}
	return normalizeManifest(manifest), nil
}

// WriteManifest 原子写入规范化后的 skills-manifest.json，保持键和数组有序以便 Git 追踪。
func WriteManifest(projectDir string, manifest Manifest) error {
	path, err := ManifestPath(projectDir)
	if err != nil {
		return err
	}
	normalized := normalizeManifest(manifest)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Skill 启用清单失败: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(path, data, atomicfile.Options{TempPrefix: ".manifest-"}); err != nil {
		if atomicfile.IsCommitted(err) {
			log.Printf("[Skills-Manifest] %s 已写入但收尾失败: %v", path, err)
			return nil
		}
		return fmt.Errorf("保存 Skill 启用清单失败: %w", err)
	}
	return nil
}

func normalizeManifest(manifest Manifest) Manifest {
	normalized := Manifest{
		Version: manifestVersion,
		Skills:  make(map[string][]string, len(manifest.Skills)),
	}
	for skillName, agents := range manifest.Skills {
		if !ValidName(skillName) {
			continue
		}
		normalized.Skills[skillName] = normalizeAgentList(agents)
	}
	return normalized
}

func normalizeAgentList(agents []string) []string {
	seen := make(map[string]struct{}, len(agents))
	result := make([]string, 0, len(agents))
	for _, agent := range agents {
		if !IsManagedAgentKey(agent) {
			continue
		}
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

// RemoveSkillFromManifest 在删除项目 Skill 时移除其在 manifest 中的条目。
func RemoveSkillFromManifest(projectDir, skillName string) error {
	manifest, err := ReadManifest(projectDir)
	if err != nil {
		return err
	}
	if _, exists := manifest.Skills[skillName]; !exists {
		return nil
	}
	delete(manifest.Skills, skillName)
	return WriteManifest(projectDir, manifest)
}

// RemoveAgentFromSkillManifest 在某个 Agent 目录删除 Skill 时同步取消其 manifest 启用状态。
func RemoveAgentFromSkillManifest(projectDir, skillName, agentKey string) error {
	if !IsManagedAgentKey(agentKey) {
		return nil
	}
	manifest, err := ReadManifest(projectDir)
	if err != nil {
		return err
	}
	current, exists := manifest.Skills[skillName]
	if !exists {
		return nil
	}
	filtered := make([]string, 0, len(current))
	for _, item := range current {
		if item != agentKey {
			filtered = append(filtered, item)
		}
	}
	manifest.Skills[skillName] = normalizeAgentList(filtered)
	return WriteManifest(projectDir, manifest)
}

// ToggleAgent 切换单个项目 Skill 在指定 Agent 上的启用状态，并立即同步对应 Agent 目录。
func ToggleAgent(opts LocationOptions, skillName, agentKey string, enabled bool) (Manifest, error) {
	if !ValidName(skillName) {
		return Manifest{}, errors.New("Skill 名称必须为小写字母、数字和连字符")
	}
	if !IsManagedAgentKey(agentKey) {
		return Manifest{}, fmt.Errorf("不支持托管同步的目标 Agent: %s", agentKey)
	}
	projectDir, err := resolveProjectDir(opts.ProjectDir)
	if err != nil {
		return Manifest{}, err
	}
	locations, err := Locations(opts)
	if err != nil {
		return Manifest{}, err
	}
	var agentLocation *Location
	for index := range locations {
		if locations[index].Key == agentKey {
			agentLocation = &locations[index]
			break
		}
	}
	if agentLocation == nil || agentLocation.ReadOnly {
		return Manifest{}, fmt.Errorf("目标 Agent 目录不可写: %s", agentKey)
	}

	projectSkillDir := filepath.Join(projectDir, skillName)
	agentSkillDir := filepath.Join(agentLocation.Path, skillName)

	manifest, err := ReadManifest(projectDir)
	if err != nil {
		return Manifest{}, err
	}

	if enabled {
		if _, statErr := os.Stat(filepath.Join(projectSkillDir, "SKILL.md")); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return Manifest{}, fmt.Errorf("项目 Skill 不存在: %s", skillName)
			}
			return Manifest{}, fmt.Errorf("检查项目 Skill 失败: %w", statErr)
		}
		if err := CopyDir(projectSkillDir, agentSkillDir, false); err != nil {
			return Manifest{}, fmt.Errorf("同步 Skill 到 %s 失败: %w", agentLocation.Agent, err)
		}
		manifest.Skills[skillName] = normalizeAgentList(append(manifest.Skills[skillName], agentKey))
	} else {
		if err := os.RemoveAll(agentSkillDir); err != nil {
			return Manifest{}, fmt.Errorf("从 %s 移除 Skill 失败: %w", agentLocation.Agent, err)
		}
		remaining := make([]string, 0, len(manifest.Skills[skillName]))
		for _, item := range manifest.Skills[skillName] {
			if item != agentKey {
				remaining = append(remaining, item)
			}
		}
		manifest.Skills[skillName] = normalizeAgentList(remaining)
	}

	if err := WriteManifest(projectDir, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// SyncManifestToAgents 按 skills-manifest.json 将项目目录中的 Skill 同步到各 Agent 目录。
// 约束：
//   - 仅处理 manifest 中登记的 Skill；未登记在 manifest 中的 Agent Skill 不受影响也不被清理。
//   - 项目目录缺失某个 manifest 登记的 Skill 时跳过该 Skill，不删除任何 Agent 副本。
//   - 复制前比较目录内容（忽略项目侧 .backups 与旧格式备份目录），一致时跳过写入。
func SyncManifestToAgents(opts LocationOptions) (SyncReport, error) {
	projectDir, err := resolveProjectDir(opts.ProjectDir)
	if err != nil {
		return SyncReport{}, err
	}
	manifest, err := ReadManifest(projectDir)
	if err != nil {
		return SyncReport{}, err
	}
	if len(manifest.Skills) == 0 {
		return SyncReport{}, nil
	}
	locations, err := Locations(opts)
	if err != nil {
		return SyncReport{}, err
	}
	locByKey := make(map[string]Location, len(locations))
	for _, location := range locations {
		if IsManagedAgentKey(location.Key) && !location.ReadOnly {
			locByKey[location.Key] = location
		}
	}

	skillNames := make([]string, 0, len(manifest.Skills))
	for skillName := range manifest.Skills {
		skillNames = append(skillNames, skillName)
	}
	sort.Strings(skillNames)

	var report SyncReport
	for _, skillName := range skillNames {
		projectSkillDir := filepath.Join(projectDir, skillName)
		if _, statErr := os.Stat(filepath.Join(projectSkillDir, "SKILL.md")); statErr != nil {
			// 项目目录缺少该 Skill（如部分克隆或被手动删除）时跳过，绝不删掉 Agent 目录已有副本。
			report.Skipped++
			continue
		}

		enabledSet := make(map[string]bool, len(manifest.Skills[skillName]))
		for _, agentKey := range manifest.Skills[skillName] {
			enabledSet[agentKey] = true
		}

		for _, agentKey := range ManagedAgentKeys {
			location, ok := locByKey[agentKey]
			if !ok {
				continue
			}
			agentSkillDir := filepath.Join(location.Path, skillName)
			if enabledSet[agentKey] {
				equal, cmpErr := skillDirsEqual(projectSkillDir, agentSkillDir)
				if cmpErr == nil && equal {
					continue
				}
				if copyErr := CopyDir(projectSkillDir, agentSkillDir, false); copyErr != nil {
					msg := fmt.Sprintf("同步 %s 到 %s 失败: %v", skillName, location.Agent, copyErr)
					log.Printf("[Skills-Sync] %s", msg)
					report.Errors = append(report.Errors, msg)
					continue
				}
				report.Synced++
			} else if _, statErr := os.Stat(agentSkillDir); statErr == nil {
				if rmErr := os.RemoveAll(agentSkillDir); rmErr != nil {
					msg := fmt.Sprintf("从 %s 移除未启用的 %s 失败: %v", location.Agent, skillName, rmErr)
					log.Printf("[Skills-Sync] %s", msg)
					report.Errors = append(report.Errors, msg)
					continue
				}
				report.Removed++
			}
		}
	}
	return report, nil
}

func skillDirsEqual(projectSkillDir, agentSkillDir string) (bool, error) {
	if _, err := os.Stat(agentSkillDir); err != nil {
		return false, err
	}
	srcHashes, err := collectSkillFileHashes(projectSkillDir, true)
	if err != nil {
		return false, err
	}
	dstHashes, err := collectSkillFileHashes(agentSkillDir, false)
	if err != nil {
		return false, err
	}
	if len(srcHashes) != len(dstHashes) {
		return false, nil
	}
	for rel, srcHash := range srcHashes {
		dstHash, ok := dstHashes[rel]
		if !ok || dstHash != srcHash {
			return false, nil
		}
	}
	return true, nil
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
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		sum, err := hashFileSHA256(current)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(rel)] = sum
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
