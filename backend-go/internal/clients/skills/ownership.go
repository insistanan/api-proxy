package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// 托管关系只存本机，不随可分享的启用清单导出。Before/After 兼容中断在文件替换两侧的恢复。
type appliedSkill struct {
	Agent  string `json:"agent"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type ownershipState struct {
	Version int                     `json:"version"`
	Entries map[string]appliedSkill `json:"entries"`
	path    string
	raw     []byte
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != manifestVersion || manifest.Skills == nil {
		return errors.New("Skill 清单必须包含当前 version 和 skills 对象")
	}
	for name, agents := range manifest.Skills {
		if !ValidName(name) {
			return fmt.Errorf("Skill 清单名称无效: %s", name)
		}
		for _, agent := range agents {
			if !IsManagedAgentKey(agent) {
				return fmt.Errorf("Skill %s 的目标未知: %s", name, agent)
			}
		}
	}
	return nil
}

func readOwnership(projectDir string) (*ownershipState, error) {
	root, err := resolveProjectDir(projectDir)
	if err != nil {
		return nil, err
	}
	state := &ownershipState{Version: 1, Entries: map[string]appliedSkill{}, path: filepath.Join(root, ".state", "skills.json")}
	state.raw, err = filetxn.Read(state.path)
	if err != nil {
		return nil, err
	}
	if state.raw == nil {
		return state, nil
	}
	// 已存在的文件必须独立验证，不能让初始化默认值掩盖 null 或缺项。
	state.Version = 0
	state.Entries = nil
	if err := json.Unmarshal(state.raw, state); err != nil {
		return nil, fmt.Errorf("读取 Skill 托管记录失败: %w", err)
	}
	if state.Version != 1 || state.Entries == nil {
		return nil, errors.New("Skill 托管记录版本或结构无效")
	}
	for key, entry := range state.Entries {
		if key != entry.Agent+":"+entry.Name || !ValidName(entry.Name) || entry.Path == "" || entry.Agent != "project" && !IsManagedAgentKey(entry.Agent) {
			return nil, fmt.Errorf("Skill 托管记录无效: %s", key)
		}
	}
	return state, nil
}

func (state *ownershipState) write() error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := filetxn.Replace(state.path, state.raw, raw, "", 0600); err != nil {
		return err
	}
	state.raw = raw
	return nil
}

func directoryFingerprint(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("Skill 路径必须是普通目录: %s", path)
	}
	hashes, err := collectSkillFileHashes(path, true)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(hashes)
	if err != nil {
		return "", err
	}
	return ContentSHA256(raw), nil
}

func (state *ownershipState) prepare(agent, name, path, next string, explicitDelete bool) error {
	identity, err := filetxn.Identity(path)
	if err != nil {
		return err
	}
	current, err := directoryFingerprint(path)
	if err != nil {
		return err
	}
	key := agent + ":" + name
	previous, owned := state.Entries[key]
	if !explicitDelete && owned && (previous.Path != identity || current != previous.Before && current != previous.After) {
		return fmt.Errorf("%w: Skill %s / %s 已被本地修改或路径改变", filetxn.ErrConflict, agent, name)
	}
	if !owned && current != "" && current != next && !explicitDelete {
		return fmt.Errorf("%w: Skill %s / %s 尚未托管", filetxn.ErrConflict, agent, name)
	}
	state.Entries[key] = appliedSkill{Agent: agent, Name: name, Path: identity, Before: current, After: next}
	return nil
}

func (state *ownershipState) confirm(agent, name string) {
	key := agent + ":" + name
	entry := state.Entries[key]
	if entry.After == "" {
		delete(state.Entries, key)
		return
	}
	entry.Before = entry.After
	state.Entries[key] = entry
}

// RebaseProjectOwnership 只调整迁移副本中项目目录的身份；外部 Agent 的身份保持原值。
func RebaseProjectOwnership(staging, source, target string) error {
	state, err := readOwnership(staging)
	if err != nil {
		return err
	}
	if state.raw == nil {
		return nil
	}
	for key, entry := range state.Entries {
		if entry.Agent != "project" {
			continue
		}
		if entry.Path != filepath.Join(source, entry.Name) {
			return fmt.Errorf("项目托管路径与迁移来源不匹配: %s", entry.Name)
		}
		entry.Path = filepath.Join(target, entry.Name)
		state.Entries[key] = entry
	}
	return state.write()
}

func ToggleAgent(opts LocationOptions, name, agent string, enabled bool) (Manifest, error) {
	if !ValidName(name) || !IsManagedAgentKey(agent) {
		return Manifest{}, errors.New("Skill 名称或目标 Agent 无效")
	}
	manifest, err := ReadManifest(opts.ProjectDir)
	if err != nil {
		return Manifest{}, err
	}
	agents := slices.DeleteFunc(slices.Clone(manifest.Skills[name]), func(value string) bool { return value == agent })
	if enabled {
		agents = append(agents, agent)
	}
	manifest.Skills[name] = normalizeAgentList(agents)
	if err := WriteManifest(opts.ProjectDir, manifest); err != nil {
		return Manifest{}, err
	}
	if _, err := syncSkillTarget(opts, name, agent, enabled); err != nil {
		return manifest, &filetxn.CommitError{Files: []string{"Skill 启用清单"}, Err: err}
	}
	return manifest, nil
}

func syncSkillTarget(opts LocationOptions, name, agent string, enabled bool) (bool, error) {
	locations, err := BaseLocations(opts)
	if err != nil {
		return false, err
	}
	index := slices.IndexFunc(locations, func(location Location) bool { return location.Key == agent })
	if index < 0 {
		return false, fmt.Errorf("目标 Agent 不可用: %s", agent)
	}
	root, err := resolveProjectDir(opts.ProjectDir)
	if err != nil {
		return false, err
	}
	state, err := readOwnership(root)
	if err != nil {
		return false, err
	}
	destination := filepath.Join(locations[index].Path, name)
	source := filepath.Join(root, name)
	if err := validateDestinations(source, []string{destination}); err != nil {
		return false, err
	}
	var next string
	if enabled {
		next, err = directoryFingerprint(source)
		if err != nil {
			return false, err
		}
		if next == "" {
			return false, fmt.Errorf("项目 Skill 不存在: %s", name)
		}
		if pending, exists := state.Entries["project:"+name]; exists && pending.Before != pending.After && pending.After != "" && pending.After != next {
			return false, fmt.Errorf("%w: 项目副本发布未完成，请重试原安装或复制操作", filetxn.ErrConflict)
		}
	} else if _, owned := state.Entries[agent+":"+name]; !owned {
		// 未曾托管的目录不属于关闭操作的清理范围。
		return false, nil
	}
	if err := state.prepare(agent, name, destination, next, false); err != nil {
		return false, err
	}
	if err := state.write(); err != nil {
		return false, err
	}
	entry := state.Entries[agent+":"+name]
	if entry.Before != next {
		if enabled {
			item, stageErr := stageCopy(source, destination, false)
			if stageErr != nil {
				return false, stageErr
			}
			defer item.abort()
			stagedRevision, revisionErr := directoryFingerprint(item.staging)
			if revisionErr != nil {
				return false, revisionErr
			}
			if item.before != entry.Before || stagedRevision != next {
				return false, fmt.Errorf("%w: Skill 暂存期间来源或目标发生变化", filetxn.ErrConflict)
			}
			err = commitInstalls([]*stagedInstall{item})
		} else {
			err = removeDirectory(destination, entry.Before)
		}
		if err != nil {
			return false, err
		}
	}
	state.confirm(agent, name)
	if err := state.write(); err != nil {
		return false, &filetxn.CommitError{Files: []string{destination}, Err: err}
	}
	return entry.Before != next, nil
}

func SyncManifestToAgents(opts LocationOptions) (SyncReport, error) {
	manifest, err := ReadManifest(opts.ProjectDir)
	if err != nil {
		return SyncReport{}, err
	}
	state, err := readOwnership(opts.ProjectDir)
	if err != nil {
		return SyncReport{}, err
	}
	var report SyncReport
	blocked := map[string]bool{}
	// 新包未发布到项目目录时，不能把旧项目内容作为本次分发来源。
	for _, entry := range state.Entries {
		if entry.Agent != "project" || entry.After == "" || entry.Before == entry.After {
			continue
		}
		current, readErr := directoryFingerprint(entry.Path)
		if readErr != nil || current != entry.After {
			blocked[entry.Name] = true
			if readErr != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: 读取待发布项目副本失败: %v", entry.Name, readErr))
			} else {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: 项目副本发布未完成，请重试原安装或复制操作", entry.Name))
			}
		}
	}
	// 只恢复已明确从启用清单删除的项目目录；复制意图尚未提交清单时不能误删。
	for key, entry := range state.Entries {
		if entry.Agent != "project" || entry.After != "" {
			continue
		}
		if _, exists := manifest.Skills[entry.Name]; exists {
			continue
		}
		root, err := resolveProjectDir(opts.ProjectDir)
		if err != nil {
			return SyncReport{}, err
		}
		path, err := filetxn.Identity(filepath.Join(root, entry.Name))
		if err != nil {
			return SyncReport{}, err
		}
		if path != entry.Path {
			return SyncReport{}, filetxn.ErrConflict
		}
		if err := removeDirectory(path, entry.Before); err != nil {
			return SyncReport{}, err
		}
		delete(state.Entries, key)
		if err := state.write(); err != nil {
			return SyncReport{}, &filetxn.CommitError{Files: []string{path}, Err: err}
		}
	}
	names := map[string]bool{}
	for name := range manifest.Skills {
		names[name] = true
	}
	for _, entry := range state.Entries {
		if entry.Agent != "project" {
			names[entry.Name] = true
		}
	}
	for name := range names {
		if blocked[name] {
			continue
		}
		for _, agent := range ManagedAgentKeys {
			enabled := slices.Contains(manifest.Skills[name], agent)
			if !enabled {
				if _, owned := state.Entries[agent+":"+name]; !owned {
					continue
				}
			}
			changed, err := syncSkillTarget(opts, name, agent, enabled)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s / %s: %v", name, agent, err))
				continue
			}
			if !changed {
				report.Skipped++
				continue
			}
			if enabled {
				report.Synced++
			} else {
				report.Removed++
			}
		}
	}
	return report, nil
}

// 删除先改名使目标路径一次性消失；清理失败保留回收目录并报告已提交。
func removeDirectory(path, expected string) error {
	current, err := directoryFingerprint(path)
	if err != nil {
		return err
	}
	if current == "" {
		return nil
	}
	if current != expected {
		return filetxn.ErrConflict
	}
	trash, err := reserveSiblingPath(filepath.Dir(path), ".skill-removed-*")
	if err != nil {
		return err
	}
	if err := atomicfile.RenameDirectory(path, trash); err != nil {
		if atomicfile.IsCommitted(err) {
			return &filetxn.CommitError{Files: []string{path}, Err: fmt.Errorf("目录已移至 %s: %w", trash, err)}
		}
		return err
	}
	if err := os.RemoveAll(trash); err != nil {
		return &filetxn.CommitError{Files: []string{path}, Err: fmt.Errorf("Skill 已移除，回收目录 %s 清理失败: %w", trash, err)}
	}
	return nil
}
