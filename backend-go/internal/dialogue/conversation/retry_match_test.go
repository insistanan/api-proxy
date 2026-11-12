package conversation

import (
	"testing"
	"time"
)

// helper：模拟一次完整的失败请求生命周期
func observeAndFail(t *testing.T, registry *Registry, obs Observation) *Record {
	t.Helper()
	rec := registry.ObserveRequest(obs)
	if rec == nil {
		t.Fatal("ObserveRequest returned nil")
	}
	registry.MarkFailure(rec.ID, obs.APIKind, "upstream error")
	registry.MarkComplete(rec.ID, obs.APIKind)
	return rec
}

// helper：模拟一次完整的成功请求生命周期
func observeAndSucceed(t *testing.T, registry *Registry, obs Observation) *Record {
	t.Helper()
	rec := registry.ObserveRequest(obs)
	if rec == nil {
		t.Fatal("ObserveRequest returned nil")
	}
	registry.MarkSuccess(rec.ID, obs.APIKind, 0, "test-channel")
	registry.MarkComplete(rec.ID, obs.APIKind)
	return rec
}

func TestRetryMatchMergesFailedDepth2(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 第一轮成功
	first := observeAndSucceed(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"第一轮"},
	})

	// 第二轮失败
	second := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2", "h3"}},
		Prompts:    []string{"第二轮"},
	})

	// 重试第二轮：同 depth、同 frontier，上次失败 → 应合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2", "h3"}},
		Prompts:    []string{"第二轮"},
	})

	if retry.ID != second.ID {
		t.Fatalf("retry should merge into failed conversation: got %s want %s", retry.ID, second.ID)
	}
	if retry.IdentitySource != "retry_match" {
		t.Fatalf("identity source = %q, want %q", retry.IdentitySource, "retry_match")
	}
	if retry.ID != first.ID {
		t.Fatalf("retry should be same conversation as first turn: got %s want %s", retry.ID, first.ID)
	}
}

func TestRetryMatchMergesFailedDepth1WithScope(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 首条消息失败
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 重试首条：同 depth=1、有 scope、上次失败 → 应合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retry.ID != first.ID {
		t.Fatalf("retry should merge into failed conversation: got %s want %s", retry.ID, first.ID)
	}
	if retry.IdentitySource != "retry_match" {
		t.Fatalf("identity source = %q, want %q", retry.IdentitySource, "retry_match")
	}
}

func TestRetryMatchDoesNotMergeDepth1WithoutScope(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "unknown", LaneHash: ""}

	// 首条消息失败，无 scope
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 重试首条：depth=1 无 scope → 不合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retry.ID == first.ID {
		t.Fatal("depth=1 without scope should not merge")
	}
}

func TestRetryMatchDoesNotMergeAfterSuccess(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 首条消息成功
	first := observeAndSucceed(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 再次发同一条消息：上次成功（LastError=""）→ 不合并
	duplicate := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if duplicate.ID == first.ID {
		t.Fatal("successful request should not be merged as retry")
	}
}

func TestRetryMatchDoesNotMergeConcurrent(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 首条消息失败
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 模拟并发：手动设置 ActiveRequests > 0
	registry.mu.Lock()
	rec := registry.records[first.ID]
	if rec != nil {
		rec.ActiveRequests = 1
	}
	registry.mu.Unlock()

	// 重试：ActiveRequests > 0 → 不合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retry.ID == first.ID {
		t.Fatal("concurrent request should not be merged as retry")
	}
}

func TestRetryMatchDoesNotMergeExpiredWindow(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 首条消息失败
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 手动将 LastCompletedAt 回退到 10 分钟前，超出 5 分钟窗口
	registry.mu.Lock()
	rec := registry.records[first.ID]
	if rec != nil {
		rec.LastCompletedAt = time.Now().Add(-10 * time.Minute)
	}
	registry.mu.Unlock()

	// 重试：超时窗口 → 不合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retry.ID == first.ID {
		t.Fatal("expired retry window should not merge")
	}
}

func TestRetryMatchDoesNotMergeDifferentScope(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()

	// 用户 A 的首条消息失败
	identityA := Identity{ClientFamily: "cursor", ScopeID: "user-a", LaneHash: "lane-main"}
	firstA := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identityA,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 用户 B 发同样内容：scope 不同 → 不合并
	identityB := Identity{ClientFamily: "cursor", ScopeID: "user-b", LaneHash: "lane-main"}
	retryB := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identityB,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retryB.ID == firstA.ID {
		t.Fatal("different scope should not merge")
	}
}

func TestRetryMatchDoesNotMergeDifferentLane(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()

	// lane-main 的首条消息失败
	identityMain := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}
	firstMain := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identityMain,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// lane-sub 发同样内容：lane 不同 → 不合并
	identitySub := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-sub"}
	retrySub := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identitySub,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	if retrySub.ID == firstMain.ID {
		t.Fatal("different lane should not merge")
	}
}

func TestRetryMatchPrefersStrictExtensionOverRetry(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 第一轮成功
	first := observeAndSucceed(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"第一轮"},
	})

	// 第二轮失败
	observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2", "h3"}},
		Prompts:    []string{"第二轮"},
	})

	// 第三轮：depth=3，transcript=[h1,h2,h3,h4]
	// 严格扩展会匹配 depth=2 的 frontier (h1,h2,h3) → 应走严格扩展
	third := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1", "h2", "h3", "h4"}},
		Prompts:    []string{"第三轮"},
	})

	if third.ID != first.ID {
		t.Fatalf("strict extension should take priority: got %s want %s", third.ID, first.ID)
	}
	if third.IdentitySource != "unique_history_extension" {
		t.Fatalf("identity source = %q, want %q", third.IdentitySource, "unique_history_extension")
	}
}

func TestRetryMatchMergesMultipleRetries(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// 首条消息失败
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})

	// 第一次重试也失败
	retry1 := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})
	if retry1.ID != first.ID {
		t.Fatalf("first retry should merge: got %s want %s", retry1.ID, first.ID)
	}

	// 第二次重试：同样应合并
	retry2 := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{"h1"}},
		Prompts:    []string{"hello"},
	})
	if retry2.ID != first.ID {
		t.Fatalf("second retry should merge: got %s want %s", retry2.ID, first.ID)
	}
	if retry2.IdentitySource != "retry_match" {
		t.Fatalf("identity source = %q, want %q", retry2.IdentitySource, "retry_match")
	}
}

func TestRetryMatchDoesNotMergeEmptyTranscript(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	// depth=0，无 transcript
	first := observeAndFail(t, registry, Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{}},
		Prompts:    []string{"hello"},
	})

	// 重试同样 depth=0 → frontierHash 为空，不合并
	retry := registry.ObserveRequest(Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: []string{}},
		Prompts:    []string{"hello"},
	})

	if retry.ID == first.ID {
		t.Fatal("empty transcript should not merge")
	}
}
