package metrics

// QueueStatus 区分已接受待写数据与容量拒收，统计缺口不能显示成完整成功。
type QueueStatus struct {
	PendingRecords int    `json:"pendingRecords"`
	PendingBytes   int    `json:"pendingBytes"`
	DroppedRecords uint64 `json:"droppedRecords"`
	LastError      string `json:"lastError,omitempty"`
}

func (s *SQLiteStore) QueueStatus() QueueStatus {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return QueueStatus{s.pendingRecords, s.pendingBytes, s.droppedRecords, s.lastWriteError}
}

func persistentRecordBytes(record PersistentRecord) int {
	// 包含结构体、切片槽及分配开销，避免只计字符串而低估待写内存。
	return 256 + len(record.MetricsKey) + len(record.BaseURL) + len(record.KeyMask) + len(record.Model) + len(record.APIType)
}
