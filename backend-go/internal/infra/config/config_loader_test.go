// 配置文件热重载：watcher 在原子替换、短暂删建、无效 JSON 与自身提交交错时的行为。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// waitForConfig 等待配置文件达到期望状态（重载是异步防抖的）。
func waitForConfig(t *testing.T, cm *ConfigManager, check func(Config) bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check(cm.GetConfig()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待配置生效超时: %s", desc)
}

// 连续备份必须分别保留完整内容，不能因时间戳相同而覆盖。
func TestConfigBackupsKeepEachSnapshot(t *testing.T) {
	dir := t.TempDir()
	manager := &ConfigManager{configFile: filepath.Join(dir, "config.json")}
	for _, content := range []string{`{"name":"first"}`, `{"name":"second"}`} {
		if err := os.WriteFile(manager.configFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := manager.backupConfig(); err != nil {
			t.Fatalf("备份失败: %v", err)
		}
	}
	backupDir := filepath.Join(dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("连续备份不能覆盖前一份: %d", len(entries))
	}
	contents := make(map[string]bool)
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(backupDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	if !contents[`{"name":"first"}`] || !contents[`{"name":"second"}`] {
		t.Fatal("两次备份都应保留各自的完整配置")
	}
}

// 清理仅操作配置备份，不能把用户文件或子目录计入删除范围。
func TestBackupCleanupKeepsUnrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < maxBackups+2; index++ {
		name := "config-2026-11-" + strconv.Itoa(10+index) + ".json"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(dir, "aaa-user-notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "config-other.json")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &ConfigManager{}
	if err := manager.cleanupOldBackups(dir); err != nil {
		t.Fatalf("清理备份失败: %v", err)
	}
	for _, path := range []string{unrelated, nested, filepath.Join(dir, "config-2026-11-12.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("非备份内容及保留范围内的备份不能删除: %s: %v", path, err)
		}
	}
	for _, name := range []string{"config-2026-11-10.json", "config-2026-11-11.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("应删除最旧的两个备份: %s: %v", name, err)
		}
	}
	if err := manager.cleanupOldBackups(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("备份目录读取失败必须显式返回错误")
	}
}

// 连续原子替换后，watcher 仍应持续工作并加载有效版本。
func TestWatcherSurvivesAtomicReplacements(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	// 外部原子替换（写临时文件 + rename，模拟编辑器或其他进程的原子保存）。
	externalWrite := func(name string) {
		t.Helper()
		cfg := cm.GetConfig()
		cfg.Upstream = append(cfg.Upstream, UpstreamConfig{
			ID: name, Name: name, Status: ChannelStatusActive, BaseURL: "https://b.example.com", APIKeys: []string{"k1"},
			MaxConcurrency: upstreamcontrol.DefaultConcurrency,
		})
		data, _ := json.MarshalIndent(cfg, "", "  ")
		tmp := filepath.Join(dir, ".external-write.tmp")
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, configFile); err != nil {
			t.Fatal(err)
		}
	}

	externalWrite("ch-1")
	waitForConfig(t, cm, func(c Config) bool { return len(c.Upstream) == 2 }, "第一次外部原子替换生效")

	externalWrite("ch-2")
	waitForConfig(t, cm, func(c Config) bool { return len(c.Upstream) == 3 }, "第二次外部原子替换生效（watcher 未失效）")

	// 再经 API 修改一次，确认自身保存路径在连续外部替换后仍正常。
	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-3", BaseURL: "https://c.example.com", APIKeys: []string{"k2"},
	}); err != nil {
		t.Fatalf("API 添加渠道失败: %v", err)
	}
	waitForConfig(t, cm, func(c Config) bool { return len(c.Upstream) == 4 }, "API 提交生效")
}

// 短暂删建（编辑器保存时先删后建）：缺失期间保留快照，恢复后正常重载。
func TestWatcherSurvivesBriefDeleteAndRecreate(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	// 删除文件：当前快照必须保留（不得触发创建默认配置覆盖用户数据）。
	if err := os.Remove(configFile); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := cm.GetConfig(); len(got.Upstream) != 1 {
		t.Fatalf("文件缺失期间快照被破坏: %+v", got.Upstream)
	}

	// 恢复（带新增渠道）：重载生效。
	cfg := cm.GetConfig()
	cfg.Upstream = append(cfg.Upstream, UpstreamConfig{
		ID: "ch-restored", Name: "ch-restored", Status: ChannelStatusActive, BaseURL: "https://r.example.com", APIKeys: []string{"k1"},
		MaxConcurrency: upstreamcontrol.DefaultConcurrency,
	})
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForConfig(t, cm, func(c Config) bool { return len(c.Upstream) == 2 }, "删建恢复后重载")
}

// 无效 JSON：保留最后有效快照，记录错误，不自动生成默认文件。
func TestInvalidJSONKeepsLastValidSnapshot(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	// 外部写入无效 JSON（截断的配置）。
	if err := os.WriteFile(configFile, []byte(`{"upstream": [ {"name": "bro`), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	if got := cm.GetConfig(); len(got.Upstream) != 1 || got.Upstream[0].Name != "ch-0" {
		t.Fatalf("无效 JSON 后快照被破坏: %+v", got.Upstream)
	}
	// 不自动生成默认文件覆盖（文件保持无效内容，等待用户修复）。
	raw, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || string(raw) == "null" {
		t.Fatal("无效内容被默认配置覆盖")
	}

	// 恢复有效内容后重载正常。
	cfg := cm.GetConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForConfig(t, cm, func(c Config) bool { return len(c.Upstream) == 1 }, "恢复有效文件后重载")
}

// 自身提交不能被 watcher 重载后再次保存，否则形成无限循环。
func TestSelfCommitDoesNotTriggerReloadLoop(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	first, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}

	// 等待远超防抖窗口 + 重载窗口：若存在"保存→重载→再保存"循环，
	// 磁盘内容会持续被改写（备份目录也会持续膨胀）。
	time.Sleep(600 * time.Millisecond)

	second, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("自身提交后磁盘内容被再次改写（存在循环保存）")
	}

	// 备份数量有界（maxBackups=10）。
	backupDir := filepath.Join(dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxBackups {
		t.Fatalf("备份数量超界: %d", len(entries))
	}
}

// 提交点前的写入失败不得发布候选：用同名目录占位配置文件使原子替换失败，内存快照保持原样。
func TestFailedCommitKeepsSnapshotConsistent(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")

	cm, err := NewConfigManager(filepath.Join(blocked, "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if _, err := cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	// 占位：删除配置文件并以同名目录占位，使原子替换必然失败
	// （rename 到目录报错）→ 提交点前失败。
	if err := os.Remove(filepath.Join(blocked, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(blocked, "config.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	before := cm.GetConfig()
	_, err = cm.AddChannel("messages", UpstreamConfig{
		Name: "ch-fail", BaseURL: "https://f.example.com", APIKeys: []string{"kf"},
	})
	if err == nil {
		t.Fatal("提交应失败（备份目录被占位文件阻塞）")
	}
	if atomicfile.IsCommitted(err) {
		// 备份失败发生在提交点之前：必须是未提交错误。
		t.Fatalf("提交点前失败不得标记已提交: %v", err)
	}

	after := cm.GetConfig()
	if len(after.Upstream) != len(before.Upstream) {
		t.Fatalf("失败提交污染内存快照: before=%d after=%d", len(before.Upstream), len(after.Upstream))
	}
	for _, up := range after.Upstream {
		if up.Name == "ch-fail" {
			t.Fatal("失败渠道出现在内存快照中")
		}
	}
}
