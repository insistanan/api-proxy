package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// InstallManaged 把文件、目标启用清单和本机托管意图串成可恢复操作。
func InstallManaged(opts LocationOptions, locations []Location, name string, files map[string][]byte, modes map[string]fs.FileMode) error {
	manifest, err := ReadManifest(opts.ProjectDir)
	if err != nil {
		return err
	}
	locations, err = installLocations(opts, locations, name, "", manifest.Skills[name])
	if err != nil {
		return err
	}
	return prepareManagedInstall(opts, locations, name, "", files, modes)
}

func CopyManaged(opts LocationOptions, locations []Location, name, source string) error {
	locations, err := installLocations(opts, locations, name, source, nil)
	if err != nil {
		return err
	}
	return prepareManagedInstall(opts, locations, name, source, nil, nil)
}

// 每份托管安装都有项目来源；导入更新同时覆盖原先启用的目标，并统一检查冲突。
func installLocations(opts LocationOptions, selected []Location, name, source string, enabled []string) ([]Location, error) {
	available, err := BaseLocations(opts)
	if err != nil {
		return nil, err
	}
	locations := slices.Clone(selected)
	for _, location := range available {
		if location.Key != "project" && !slices.Contains(enabled, location.Key) {
			continue
		}
		if slices.ContainsFunc(locations, func(item Location) bool { return item.Key == location.Key }) {
			continue
		}
		if source != "" && location.Key == "project" {
			identity, err := filetxn.Identity(source)
			if err != nil {
				return nil, err
			}
			projectIdentity, err := filetxn.Identity(filepath.Join(location.Path, name))
			if err != nil {
				return nil, err
			}
			if identity == projectIdentity {
				continue
			}
		}
		locations = append(locations, location)
	}
	// 项目来源先发布，中断后可由托管记录继续分发。
	slices.SortStableFunc(locations, func(left, right Location) int {
		if left.Key == right.Key {
			return 0
		}
		if left.Key == "project" {
			return -1
		}
		if right.Key == "project" {
			return 1
		}
		return 0
	})
	return locations, nil
}

func prepareManagedInstall(opts LocationOptions, locations []Location, name, source string, files map[string][]byte, modes map[string]fs.FileMode) error {
	if !ValidName(name) {
		return errors.New("Skill 名称无效")
	}
	manifest, err := ReadManifest(opts.ProjectDir)
	if err != nil {
		return err
	}
	state, err := readOwnership(opts.ProjectDir)
	if err != nil {
		return err
	}
	staged, err := stageInstallTargets(locations, name, source, files, modes)
	if err != nil {
		return err
	}
	var destinations []string
	var packageRevision string
	defer func() {
		for _, item := range staged {
			item.abort()
		}
	}()
	for _, item := range staged {
		location := item.location
		destinations = append(destinations, item.destination)
		next, err := directoryFingerprint(item.staging)
		if err != nil {
			return err
		}
		if packageRevision != "" && next != packageRevision {
			return fmt.Errorf("%w: 多目标暂存期间 Skill 来源发生变化", filetxn.ErrConflict)
		}
		packageRevision = next
		if err := state.prepare(location.Key, name, item.destination, next, false); err != nil {
			return err
		}
		if state.Entries[location.Key+":"+name].Before != item.before {
			return fmt.Errorf("%w: Skill 暂存期间目标发生变化", filetxn.ErrConflict)
		}
		if IsManagedAgentKey(location.Key) {
			manifest.Skills[name] = normalizeAgentList(append(manifest.Skills[name], location.Key))
		} else if location.Key == "project" && manifest.Skills[name] == nil {
			manifest.Skills[name] = []string{}
		}
	}
	if err := state.write(); err != nil {
		return err
	}
	if err := WriteManifest(opts.ProjectDir, manifest); err != nil {
		return &filetxn.CommitError{Files: []string{"Skill 待分发记录"}, Err: err}
	}
	if err := commitInstalls(staged); err != nil {
		return &filetxn.CommitError{Files: []string{"Skill 启用清单和待分发记录"}, Err: err}
	}
	for _, location := range locations {
		state.confirm(location.Key, name)
	}
	if err := state.write(); err != nil {
		return &filetxn.CommitError{Files: destinations, Err: err}
	}
	return nil
}

// DeleteManaged 是用户明确选中一个目录的删除操作。自动同步不使用显式接管权限。
func DeleteManaged(opts LocationOptions, ref Item) error {
	if ref.ReadOnly || !ValidName(ref.Name) {
		return errors.New("Skill 不可删除")
	}
	manifest, err := ReadManifest(opts.ProjectDir)
	if err != nil {
		return err
	}
	state, err := readOwnership(opts.ProjectDir)
	if err != nil {
		return err
	}
	if err := state.prepare(ref.LocationKey, ref.Name, ref.Path, "", true); err != nil {
		return err
	}
	if ref.LocationKey == "project" {
		delete(manifest.Skills, ref.Name)
	} else if agents, exists := manifest.Skills[ref.Name]; exists {
		manifest.Skills[ref.Name] = slices.DeleteFunc(agents, func(agent string) bool { return agent == ref.LocationKey })
	}
	if err := state.write(); err != nil {
		return err
	}
	if err := WriteManifest(opts.ProjectDir, manifest); err != nil {
		return &filetxn.CommitError{Files: []string{"Skill 待删除记录"}, Err: err}
	}
	entry := state.Entries[ref.LocationKey+":"+ref.Name]
	if err := removeDirectory(ref.Path, entry.Before); err != nil {
		return &filetxn.CommitError{Files: []string{"Skill 启用清单"}, Err: err}
	}
	state.confirm(ref.LocationKey, ref.Name)
	if err := state.write(); err != nil {
		return &filetxn.CommitError{Files: []string{ref.Path}, Err: err}
	}
	return nil
}
