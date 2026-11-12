package channelcrud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

func TestPromotionInputValidationBeforeWrite(t *testing.T) {
	for _, input := range []struct {
		body   string
		status int
	}{
		{`{"duration":-1,"count":1}`, http.StatusBadRequest},
		{`{"duration":1,"count":-1}`, http.StatusBadRequest},
		{`{"duration":9223372037,"count":1}`, http.StatusBadRequest},
		{`{"duration":60,"count":2}`, http.StatusOK},
		{`{"duration":0,"count":0}`, http.StatusOK},
	} {
		t.Run(input.body, func(t *testing.T) {
			called := false
			h := New(Ops{SetPromotion: func(_ int, duration time.Duration, count int) error {
				called = true
				requireDuration := time.Duration(0)
				if input.body == `{"duration":60,"count":2}` {
					requireDuration = time.Minute
					if count != 2 {
						t.Fatalf("次数 = %d", count)
					}
				}
				if duration != requireDuration {
					t.Fatalf("时长 = %v", duration)
				}
				return nil
			}}, nil)
			rr := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rr)
			ctx.Params = gin.Params{{Key: "id", Value: "0"}}
			ctx.Request = httptest.NewRequest(http.MethodPost, "/0/promotion", strings.NewReader(input.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			h.SetChannelPromotion(ctx)
			if rr.Code != input.status || called != (input.status == http.StatusOK) {
				t.Fatalf("状态 = %d, 写入 = %t, 响应 = %s", rr.Code, called, rr.Body.String())
			}
		})
	}
}

func TestCommittedOperationsFinishSchedulerCleanup(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		committed bool
	}{
		{"success", nil, false},
		{"before-commit", errors.New("备份失败"), false},
		{"after-commit", &atomicfile.CommittedError{Phase: atomicfile.PhaseDirSync, Err: errors.New("目录同步失败")}, true},
	}
	for _, operation := range []string{"update", "delete", "activate", "suspend"} {
		for _, tc := range cases {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := cm.Close(); err != nil {
						t.Error(err)
					}
				})
				up := config.UpstreamConfig{Name: "test", BaseURL: "https://example.com", APIKeys: []string{"test-key"}, ServiceType: "openai"}
				if err := cm.AddUpstream(up); err != nil {
					t.Fatal(err)
				}
				mm := metrics.NewMetricsManager()
				trace := session.NewTraceAffinityManager()
				sch := scheduler.NewChannelScheduler(cm, mm, nil, nil, nil, nil, nil, nil, trace, nil)
				t.Cleanup(sch.Stop)
				breaker := circuit.NewManager(circuit.Config{FailureThreshold: 1, Cooldown: time.Hour}, true)
				sch.SetCircuitManager(breaker)
				breaker.RecordFailure("messages", 0)
				breaker.RecordFailure("responses", 0)
				mm.RecordFailure(up.BaseURL, up.APIKeys[0], 0)
				trace.SetPreferredChannelForKind("messages", "conversation", 0)
				h := New(Ops{
					Kind:      scheduler.ChannelKindMessages,
					Update:    func(int, config.UpstreamUpdate) (bool, error) { return true, tc.err },
					Remove:    func(int) (*config.UpstreamConfig, error) { return &up, tc.err },
					SetStatus: func(int, string) error { return tc.err },
				}, sch)
				rr := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(rr)
				ctx.Params = gin.Params{{Key: "id", Value: "0"}}
				ctx.Request = httptest.NewRequest(http.MethodPatch, "/0", strings.NewReader(`{"name":"test"}`))
				ctx.Request.Header.Set("Content-Type", "application/json")
				if operation == "update" {
					h.UpdateUpstream(ctx)
				} else if operation == "delete" {
					h.DeleteUpstream(ctx)
				} else {
					status := "active"
					if operation == "suspend" {
						status = "suspended"
					}
					ctx.Request = httptest.NewRequest(http.MethodPatch, "/0/status", strings.NewReader(`{"status":"`+status+`"}`))
					ctx.Request.Header.Set("Content-Type", "application/json")
					h.SetChannelStatus(ctx)
				}
				wantStatus := http.StatusOK
				if tc.err != nil {
					wantStatus = http.StatusInternalServerError
				}
				if rr.Code != wantStatus {
					t.Fatalf("响应状态不符: got=%d want=%d body=%s", rr.Code, wantStatus, rr.Body.String())
				}
				var body struct {
					Code      string `json:"code"`
					Committed bool   `json:"committed"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Committed != tc.committed || (tc.committed && body.Code != "CONFIG_COMMITTED") {
					t.Fatalf("已提交标记不符: %+v", body)
				}
				applied := tc.err == nil || tc.committed
				_, affinityExists := trace.GetPreferredChannelForKind("messages", "conversation")
				if operation == "delete" {
					count := mm.GetChannelRequestCount(up.BaseURL, up.APIKeys, 0)
					if (count == 0) != applied || affinityExists == applied {
						t.Fatalf("删除清理时机不符: count=%d affinity=%v applied=%v", count, affinityExists, applied)
					}
				} else {
					reset := applied && operation != "suspend"
					rate := mm.CalculateChannelFailureRate(up.BaseURL, up.APIKeys, 0)
					if (rate == 0) != reset || !affinityExists {
						t.Fatalf("更新重置时机不符: failureRate=%v affinity=%v applied=%v", rate, affinityExists, applied)
					}
					if snap := breaker.SnapshotEntry("messages", 0); (snap.State == circuit.StateClosed) != reset {
						t.Fatalf("渠道级熔断重置时机不符: %+v, reset=%v", snap, reset)
					}
					if count := mm.GetChannelRequestCount(up.BaseURL, up.APIKeys, 0); count != 1 {
						t.Fatalf("恢复熔断不应清空历史请求数: %d", count)
					}
				}
				if snap := breaker.SnapshotEntry("responses", 0); snap.State != circuit.StateOpen {
					t.Fatalf("不应重置其他协议的同索引渠道: %+v", snap)
				}
			})
		}
	}
}
