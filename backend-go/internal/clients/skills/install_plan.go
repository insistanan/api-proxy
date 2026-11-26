package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// validateDestinations 在创建暂存目录前核对真实路径，拒绝别名、重复及相互包含。
func validateDestinations(source string, destinations []string) error {
	for index, destination := range destinations {
		if source != "" {
			overlap, err := filetxn.Overlap(source, destination)
			if err != nil {
				return err
			}
			if overlap {
				return fmt.Errorf("Skill 来源与目标相同或互相包含: %s / %s", source, destination)
			}
		}
		for _, previous := range destinations[:index] {
			overlap, err := filetxn.Overlap(previous, destination)
			if err != nil {
				return err
			}
			if overlap {
				return fmt.Errorf("Skill 目标重复或互相包含: %s / %s", previous, destination)
			}
		}
	}
	return nil
}

func commitInstalls(staged []*stagedInstall) error {
	// 文件已逐个 Sync；发布目录前还需同步新建子目录中的目录项。
	for _, item := range staged {
		if err := syncInstallDirectories(item.staging); err != nil {
			return err
		}
	}
	committed := make([]*stagedInstall, 0, len(staged))
	for _, item := range staged {
		if err := item.commit(); err != nil {
			failures := []error{err}
			var remaining []string
			if item.committed {
				committed = append(committed, item)
			}
			for index := len(committed) - 1; index >= 0; index-- {
				previous := committed[index]
				if rollbackErr := previous.rollback(); rollbackErr != nil {
					failures = append(failures, rollbackErr)
					remaining = append(remaining, previous.destination)
				}
			}
			if len(remaining) > 0 {
				return &filetxn.CommitError{Files: remaining, Err: errors.Join(failures...)}
			}
			return errors.Join(failures...)
		}
		committed = append(committed, item)
	}
	var failures []error
	var paths []string
	for _, item := range committed {
		paths = append(paths, item.destination)
		if err := item.cleanup(); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return &filetxn.CommitError{Files: paths, Err: errors.Join(failures...)}
	}
	return nil
}

func syncInstallDirectories(root string) error {
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	}); err != nil {
		return err
	}
	slices.Reverse(directories)
	for _, path := range directories {
		if err := atomicfile.SyncDirectory(path); err != nil {
			return err
		}
	}
	return nil
}
