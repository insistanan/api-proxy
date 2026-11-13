package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/insistanan/api-proxy/internal/storage/logbody"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSharedBodyRoundTripAndPurge(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	body, err := json.Marshal(map[string]string{"text": strings.Repeat("正文", 5000)})
	require.NoError(t, err)
	now := time.Now()
	later := now.AddDate(0, 0, 1)
	for _, entry := range []TrafficLog{
		{RequestID: "today", UnixMilli: now.UnixMilli(), Phase: PhaseClientRequest, Body: string(body)},
		{RequestID: "later", UnixMilli: later.UnixMilli(), Phase: PhaseUpstreamRequest, Body: string(body)},
	} {
		require.NoError(t, store.RecordTraffic(entry))
	}
	require.NoError(t, store.Flush())
	var bodies, references int
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_bodies`).Scan(&bodies))
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_body_refs`).Scan(&references))
	require.Equal(t, 1, bodies)
	require.Equal(t, 2, references)
	normalized, _, _ := logbody.PrepareBody(body)
	rows, err := store.QueryTrafficLogs(context.Background(), QueryOptions{To: math.MaxInt64})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, normalized, row.Body)
	}
	reader, err := OpenReadOnly(store.Path())
	require.NoError(t, err)
	readonlyRows, err := reader.QueryTrafficByRequestID(context.Background(), "later")
	require.NoError(t, err)
	require.Equal(t, normalized, readonlyRows[0].Body)
	require.NoError(t, reader.Close())
	_, err = store.Purge(context.Background(), PurgeOptions{Date: now.Format("2006-01-02")})
	require.NoError(t, err)
	remaining, err := store.QueryTrafficByRequestID(context.Background(), "later")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, normalized, remaining[0].Body)
	_, err = store.Purge(context.Background(), PurgeOptions{All: true})
	require.NoError(t, err)
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_bodies`).Scan(&bodies))
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_body_refs`).Scan(&references))
	require.Zero(t, bodies)
	require.Zero(t, references)
}

func TestMissingReferencedBodyReturnsError(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "broken", Phase: PhaseUpstreamResponse, Body: strings.Repeat("x", 5000)}))
	require.NoError(t, store.Flush())
	_, err = store.db.Exec(`DELETE FROM traffic_bodies`)
	require.NoError(t, err)
	_, err = store.QueryTrafficByRequestID(context.Background(), "broken")
	require.Error(t, err)
}

func TestReadOnlyLegacyDatabaseWithoutBodyTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	// 只在临时库中模拟旧版本结构，验证只读打开不会要求迁移。
	for _, statement := range []string{`DROP TRIGGER delete_traffic_body_ref`, `DROP TABLE traffic_body_refs`, `DROP TABLE traffic_bodies`} {
		_, err = db.Exec(statement)
		require.NoError(t, err)
	}
	body := strings.Repeat("legacy", 1000)
	_, err = db.Exec(`INSERT INTO traffic_logs (unix_milli, timestamp, request_id, phase, body) VALUES (?, ?, ?, ?, ?)`, time.Now().UnixMilli(), time.Now().Format(time.RFC3339Nano), "legacy", PhaseClientRequest, body)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	reader, err := OpenReadOnly(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	entries, err := reader.QueryTrafficByRequestID(context.Background(), "legacy")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, body, entries[0].Body)
}

func TestBodyCleanupRetainsLiveReference(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	body := strings.Repeat("same", 2000)
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "kept", Phase: PhaseClientRequest, Body: body}))
	require.NoError(t, store.Flush())
	old := RetentionCutoff(time.Now()).Add(-time.Hour).UnixMilli()
	_, err = store.db.Exec(`UPDATE traffic_bodies SET created_at = ?`, old)
	require.NoError(t, err)
	_, _, err = store.cleanupExpired()
	require.NoError(t, err)
	kept, err := store.QueryTrafficByRequestID(context.Background(), "kept")
	require.NoError(t, err)
	require.Equal(t, body, kept[0].Body)
	_, err = store.db.Exec(`DELETE FROM traffic_logs WHERE request_id = 'kept'`)
	require.NoError(t, err)
	_, _, err = store.cleanupExpired()
	require.NoError(t, err)
	var count int
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_bodies`).Scan(&count))
	require.Zero(t, count)
}

func TestLifecycleSummaryUsesFinalOutcome(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	start := RequestLifecycle{ConversationID: "conversation", RequestHash: "hash", Model: "model", BootID: store.BootID(), StartedAt: time.Now().UnixMilli()}
	end := start
	end.Status, end.ErrorType, end.ErrorMessage = "failed", "network", "等待响应头超时"
	duration := int64(42)
	end.EndedAt, end.DurationMs = start.StartedAt+duration, &duration
	for _, record := range []struct {
		phase string
		state RequestLifecycle
	}{{PhaseRequestStart, start}, {PhaseRequestEnd, end}} {
		body, marshalErr := json.Marshal(record.state)
		require.NoError(t, marshalErr)
		require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "req", Phase: record.phase, Body: string(body)}))
	}
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "req", Phase: PhaseUpstreamResponse, StatusCode: 429}))
	require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "req", Payload: []byte(`{"status":"failed","errorType":"quota","channelName":"first"}`)}))
	require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "req", Payload: []byte(`{"status":"failed","errorType":"network","channelName":"last"}`)}))
	require.NoError(t, store.Flush())
	detail, err := store.GetSystemLogDetail(context.Background(), "req")
	require.NoError(t, err)
	require.Equal(t, "last", detail.Summary.ChannelName)
	require.Equal(t, "network", detail.Summary.ErrorType)
	require.Equal(t, "failed", detail.Summary.Status)
	require.Zero(t, detail.Summary.StatusCode)
	require.Equal(t, "conversation", detail.Summary.ConversationID)
	require.Equal(t, end.EndedAt, detail.Summary.EndedAt)
	require.Equal(t, &duration, detail.Summary.DurationMs)
	list, err := store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{})
	require.NoError(t, err)
	require.Equal(t, detail.Summary, list[0])
}

func TestWebDetailRejectsOversizedBodiesButCLIQueryStillWorks(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	for range 5 {
		require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "large", Phase: PhaseUpstreamResponse, Body: strings.Repeat("x", logbody.MaxBodyBytes)}))
	}
	require.NoError(t, store.Flush())
	_, err = store.GetSystemLogDetail(context.Background(), "large")
	require.ErrorIs(t, err, ErrLogDetailTooLarge)
	rows, err := store.QueryTrafficByRequestID(context.Background(), "large")
	require.NoError(t, err)
	require.Len(t, rows, 5)
}

func TestWebDetailRejectsTooManyTrafficOrAttemptRows(t *testing.T) {
	for _, attempts := range []bool{false, true} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			count := maxDetailTrafficRows + 1
			if attempts {
				count = maxDetailRequestRows + 1
			}
			for range count {
				if attempts {
					require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "many", Payload: []byte(`{"status":"failed"}`)}))
				} else {
					require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "many", Phase: PhaseUpstreamHeaders, StatusCode: 200}))
				}
			}
			require.NoError(t, store.Flush())
			_, err = store.GetSystemLogDetail(context.Background(), "many")
			require.ErrorIs(t, err, ErrLogDetailTooLarge)
		})
	}
}

func TestLifecycleSummaryDistinguishesActiveAndPreviousProcess(t *testing.T) {
	for _, test := range []struct{ name, boot, want string }{{"active", "current", "processing"}, {"previous process", "old", "interrupted"}} {
		t.Run(test.name, func(t *testing.T) {
			rows := []trafficRowMeta{{Phase: PhaseRequestStart, Lifecycle: &RequestLifecycle{BootID: test.boot}}}
			summary := buildSystemLogSummary("request", rows, nil)
			store := &Store{bootID: "current"}
			store.adjustLifecycleStatus(&summary, rows)
			require.Equal(t, test.want, summary.Status)
		})
	}
}
