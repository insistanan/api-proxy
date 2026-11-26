package mcpsync

import (
	"fmt"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// AppliedEntry 记录实际文件身份及提交前后的完整条目指纹。
// 先落意图再分发；失败或重启后仍保留删除范围，也可识别已写入但尚未确认的结果。
type AppliedEntry struct {
	Agent  AgentKey `json:"agent"`
	Path   string   `json:"path"`
	Name   string   `json:"name"`
	Before string   `json:"before"`
	After  string   `json:"after"`
}

func entryFingerprints(adapter Adapter, raw []byte) (map[string]string, error) {
	root, field, err := configObject(adapter, raw)
	if err != nil {
		return nil, err
	}
	entries, err := objectField(root, field)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(entries))
	for name, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("MCP 条目 %s 必须是对象", name)
		}
		encoded, err := encodeEntry(adapter, name, object)
		if err != nil {
			return nil, err
		}
		result[name] = Revision(encoded)
	}
	return result, nil
}

func managedEntries(file storeFile, agent AgentKey, desired map[string]Server) map[string]struct{} {
	managed := make(map[string]struct{})
	for name := range desired {
		managed[name] = struct{}{}
	}
	for _, entry := range file.Applied {
		if entry.Agent == agent {
			managed[entry.Name] = struct{}{}
		}
	}
	return managed
}

func checkOwnership(file storeFile, adapter Adapter, path string, current []byte, managed map[string]struct{}, desired map[string]Server, adopted map[string]Server) error {
	identity, err := filetxn.Identity(path)
	if err != nil {
		return err
	}
	fingerprints, err := entryFingerprints(adapter, current)
	if err != nil {
		return err
	}
	entries, err := adapter.Read(current)
	if err != nil {
		return err
	}
	for name := range managed {
		if _, explicit := adopted[name]; explicit {
			continue
		}
		index := slices.IndexFunc(file.Applied, func(entry AppliedEntry) bool { return entry.Agent == adapter.Key() && entry.Name == name })
		if index >= 0 {
			entry := file.Applied[index]
			if identity != entry.Path || fingerprints[name] != entry.Before && fingerprints[name] != entry.After {
				return fmt.Errorf("%w: %s / %s 已在本地修改或路径改变", ErrAgentConflict, adapter.Name(), name)
			}
			continue
		}
		if fingerprints[name] == "" {
			continue
		}
		wanted, ok := desired[name]
		if !ok {
			return fmt.Errorf("%w: %s 未登记托管，不能删除", ErrAgentConflict, name)
		}
		matched := slices.ContainsFunc(entries, func(entry DetectedServer) bool {
			return entry.Name == name && entry.Server != nil && sameConfig(*entry.Server, wanted)
		})
		if !matched {
			return fmt.Errorf("%w: %s / %s，请显式接管", ErrAgentConflict, adapter.Name(), name)
		}
	}
	return nil
}

func trackOwnership(file *storeFile, adapter Adapter, path string, current, next []byte, managed map[string]struct{}) error {
	identity, err := filetxn.Identity(path)
	if err != nil {
		return err
	}
	before, err := entryFingerprints(adapter, current)
	if err != nil {
		return err
	}
	after, err := entryFingerprints(adapter, next)
	if err != nil {
		return err
	}
	for name := range managed {
		file.Applied = slices.DeleteFunc(file.Applied, func(entry AppliedEntry) bool { return entry.Agent == adapter.Key() && entry.Name == name })
		file.Applied = append(file.Applied, AppliedEntry{Agent: adapter.Key(), Path: identity, Name: name, Before: before[name], After: after[name]})
	}
	return nil
}

func confirmOwnership(file *storeFile, agent AgentKey, names ...string) {
	confirmed := func(entry AppliedEntry) bool {
		return entry.Agent == agent && (len(names) == 0 || slices.Contains(names, entry.Name))
	}
	for index := range file.Applied {
		if confirmed(file.Applied[index]) {
			file.Applied[index].Before = file.Applied[index].After
		}
	}
	file.Applied = slices.DeleteFunc(file.Applied, func(entry AppliedEntry) bool { return confirmed(entry) && entry.After == "" })
}
