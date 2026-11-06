// 正文引用只影响存储；Web 与 CLI 始终读取还原后的独立正文。
package logger

func (s *Store) trafficSelect() string {
	body := "t.body"
	join := ""
	if s.bodyRefs {
		// 引用存在而正文丢失时返回 NULL，让 Scan 明确报错，不静默返回空正文。
		body = "CASE WHEN r.traffic_id IS NULL THEN t.body ELSE b.body END"
		join = " LEFT JOIN traffic_body_refs r ON r.traffic_id = t.id LEFT JOIN traffic_bodies b ON b.body_hash = r.body_hash"
	}
	return `SELECT t.id, t.unix_milli, t.timestamp, t.request_id, t.attempt_id, t.phase, t.api_type, t.method, t.url,
		t.status_code, t.stream, t.headers_json, ` + body + `, t.truncated, t.original_bytes FROM traffic_logs t` + join
}
