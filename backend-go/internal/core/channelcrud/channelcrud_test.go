package channelcrud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/gin-gonic/gin"
)

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
	for _, operation := range []string{"update", "delete"} {
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
				sch := scheduler.NewChannelScheduler(cm, mm, nil, nil, nil, nil, trace, nil)
				t.Cleanup(sch.Stop)
				mm.RecordFailure(up.BaseURL, up.APIKeys[0], 0)
				trace.SetPreferredChannelForKind("messages", "conversation", 0)
				h := New(Ops{
					Kind:   scheduler.ChannelKindMessages,
					Update: func(int, config.UpstreamUpdate) (bool, error) { return true, tc.err },
					Remove: func(int) (*config.UpstreamConfig, error) { return &up, tc.err },
				}, sch)
				rr := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(rr)
				ctx.Params = gin.Params{{Key: "id", Value: "0"}}
				ctx.Request = httptest.NewRequest(http.MethodPatch, "/0", strings.NewReader(`{"name":"test"}`))
				ctx.Request.Header.Set("Content-Type", "application/json")
				if operation == "update" {
					h.UpdateUpstream(ctx)
				} else {
					h.DeleteUpstream(ctx)
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
					rate := mm.CalculateChannelFailureRate(up.BaseURL, up.APIKeys, 0)
					if (rate == 0) != applied || !affinityExists {
						t.Fatalf("更新重置时机不符: failureRate=%v affinity=%v applied=%v", rate, affinityExists, applied)
					}
				}
			})
		}
	}
}
