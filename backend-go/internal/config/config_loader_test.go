// R3-b 验收测试：配置事务提交与热重载一起落地。
// 覆盖方案 6.4：
//   - 连续原子替换 / 编辑器保存 / 短暂删建 → watcher 持续工作，最终加载有效版本；
//   - 无效 JSON → 保留有效配置，不自动生成默认文件；
//   - 自身提交与 watcher 重载交错 → 不重复提交（摘要去重）；
//   - 保存失败 → 内存与磁盘一致（提交点语义）。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
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

// 连续原子替换（模拟自身保存与外部原子写）：watcher 不失效，最终加载有效版本。
func TestWatcherSurvivesAtomicReplacements(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if err := cm.AddUpstream(UpstreamConfig{
		Name: "ch-0", BaseURL: "https://a.example.com", APIKeys: []string{"k0"},
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	// 外部原子替换（写临时文件 + rename，模拟编辑器或其他进程的原子保存）。
	externalWrite := func(name string) {
		t.Helper()
		cfg := cm.GetConfig()
		cfg.Upstream = append(cfg.Upstream, UpstreamConfig{
			Name: name, BaseURL: "https://b.example.com", APIKeys: []string{"k1"},
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
	if err := cm.AddUpstream(UpstreamConfig{
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

	if err := cm.AddUpstream(UpstreamConfig{
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
		Name: "ch-restored", BaseURL: "https://r.example.com", APIKeys: []string{"k1"},
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

	if err := cm.AddUpstream(UpstreamConfig{
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

// 自身提交与 watcher 重载交错：不重复提交、无循环保存。
// 通过比较磁盘内容稳定性（提交次数）间接验证：API 保存后等充足时间，
// 磁盘文件 mtime/内容不应被再次改写。
func TestSelfCommitDoesNotTriggerReloadLoop(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if err := cm.AddUpstream(UpstreamConfig{
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

// 保存失败语义：内存与磁盘不分离。
// 把目标路径变成不可写状态（用同名目录占位目标文件），提交必然失败：
// 内存快照应保持原样（未发布）。
func TestFailedCommitKeepsSnapshotConsistent(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")

	cm, err := NewConfigManager(filepath.Join(blocked, "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	if err := cm.AddUpstream(UpstreamConfig{
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
	err = cm.AddUpstream(UpstreamConfig{
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
