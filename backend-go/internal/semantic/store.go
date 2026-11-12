package semantic

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/sqlitedb"
)

var ErrNotFound = errors.New("数据集不存在")

type Service struct {
	db        *sql.DB
	embedder  Embedder
	mu        sync.Mutex
	datasets  map[string]Dataset
	indexes   map[string]*snapshot
	buildSlot chan struct{}
}

func New(path string, embedder Embedder) (*Service, error) {
	if embedder == nil {
		return nil, fmt.Errorf("数据集服务需要向量计算器")
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, err
	}
	s := &Service{db: db, embedder: embedder, datasets: map[string]Dataset{}, indexes: map[string]*snapshot{}, buildSlot: make(chan struct{}, 1)}
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS semantic_datasets(id TEXT PRIMARY KEY, data BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS semantic_vectors(id TEXT PRIMARY KEY, data BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS semantic_indexes(id TEXT PRIMARY KEY, data BLOB NOT NULL, vectors BLOB NOT NULL)`,
	} {
		if _, err = db.Exec(query); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	if err = s.load(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

func (s *Service) Close() error { return s.db.Close() }

func (s *Service) load() error {
	rows, err := s.db.Query(`SELECT data FROM semantic_datasets`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		var d Dataset
		if err = json.Unmarshal(data, &d); err != nil {
			break
		}
		if err = ValidateDataset(d); err != nil {
			break
		}
		s.datasets[d.ID] = d
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func (s *Service) Get(id string) (Dataset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		return Dataset{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return cloneDataset(d), nil
}

// Save 使用修订号阻止并发编辑互相覆盖；编辑不会替换已发布的向量快照。
func (s *Service) Save(ctx context.Context, d Dataset) (Dataset, error) {
	if err := ValidateDataset(d); err != nil {
		return Dataset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.datasets[d.ID]
	if d.Revision != previous.Revision {
		return Dataset{}, fmt.Errorf("数据集已被其他操作修改，请重新加载后编辑")
	}
	if exists && previous.Kind != d.Kind {
		return Dataset{}, fmt.Errorf("已有数据集不能更改用途")
	}
	if !exists && len(s.datasets) >= 32 {
		return Dataset{}, fmt.Errorf("数据集数量已达 32 个上限")
	}
	d = cloneDataset(d)
	d.Revision++
	data, err := json.Marshal(d)
	if err != nil {
		return Dataset{}, err
	}
	if len(data) > maxDatasetBytes {
		return Dataset{}, fmt.Errorf("数据集不能超过 4 MiB")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(data)),0) FROM semantic_datasets WHERE id<>?`, d.ID).Scan(&total); err != nil {
		return Dataset{}, err
	}
	if total+int64(len(data)) > maxDatasetTotalBytes {
		return Dataset{}, fmt.Errorf("全部数据集超过 64 MiB，请先移除不再使用的样本或数据集")
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO semantic_datasets(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, d.ID, data); err != nil {
		return Dataset{}, fmt.Errorf("保存数据集失败: %w", err)
	}
	s.datasets[d.ID] = d
	return cloneDataset(d), nil
}

func (s *Service) List(ctx context.Context) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.datasets))
	for _, d := range s.datasets {
		item := Summary{ID: d.ID, Name: d.Name, Kind: d.Kind, Revision: d.Revision, Count: len(d.Samples), Labels: []string{}, Indexes: []IndexStatus{}}
		labels := map[string]bool{}
		for _, sample := range d.Samples {
			labels[sample.Label] = true
			if sample.Approved {
				item.Approved++
			}
		}
		for label := range labels {
			item.Labels = append(item.Labels, label)
		}
		sort.Strings(item.Labels)
		out = append(out, item)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM semantic_indexes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		var index IndexStatus
		if err = json.Unmarshal(data, &index); err != nil {
			break
		}
		for i := range out {
			if out[i].ID == index.DatasetID {
				out[i].Indexes = append(out[i].Indexes, index)
			}
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, len(vector)*4)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	return data
}

func decodeVector(data []byte) ([]float32, error) {
	if len(data) == 0 || len(data)%4 != 0 || len(data)/4 > 4096 {
		return nil, fmt.Errorf("持久化向量维度无效")
	}
	vector := make([]float32, len(data)/4)
	var norm float64
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		norm += float64(vector[i]) * float64(vector[i])
	}
	if math.IsNaN(norm) || math.IsInf(norm, 0) || math.Abs(norm-1) > 0.001 {
		return nil, fmt.Errorf("持久化向量不是有效单位向量")
	}
	return vector, nil
}

func normalized(vector []float64) ([]float32, error) {
	if len(vector) == 0 || len(vector) > 4096 {
		return nil, fmt.Errorf("向量维度为空或超过 4096")
	}
	var norm float64
	for _, n := range vector {
		norm += n * n
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("向量包含无效数值")
	}
	norm = math.Sqrt(norm)
	out := make([]float32, len(vector))
	for i, n := range vector {
		out[i] = float32(n / norm)
	}
	return out, nil
}

func (s *Service) published(ctx context.Context, id string) (*snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached := s.indexes[id]; cached != nil {
		return cached, nil
	}
	var data, vectors []byte
	err := s.db.QueryRowContext(ctx, `SELECT data,vectors FROM semantic_indexes WHERE id=?`, id).Scan(&data, &vectors)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("数据集尚未为当前模型建立索引，请先构建并发布")
	}
	if err != nil {
		return nil, err
	}
	var index snapshot
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index.Count != len(index.Samples) || index.Dimension < 1 || index.Dimension > 4096 || len(vectors) != index.Count*index.Dimension*4 {
		return nil, fmt.Errorf("持久化索引不完整")
	}
	for offset := 0; offset < len(vectors); offset += index.Dimension * 4 {
		vector, err := decodeVector(vectors[offset : offset+index.Dimension*4])
		if err != nil {
			return nil, err
		}
		index.Vectors = append(index.Vectors, vector)
	}
	// 内存缓存只影响读盘次数，不会删除磁盘中的已发布索引。
	usage := func() int {
		total := 0
		for _, cached := range s.indexes {
			total += cached.Count * cached.Dimension * 4
			for _, row := range cached.Samples {
				total += len(row.Text)
			}
		}
		return total
	}
	for len(s.indexes) > 0 && (len(s.indexes) >= 8 || usage()+len(vectors)+len(data) > maxMemoryIndexBytes) {
		for key := range s.indexes {
			delete(s.indexes, key)
			break
		}
	}
	s.indexes[id] = &index
	return &index, nil
}

// Delete 显式删除数据集及其已发布索引。正在构建的任务无法再发布已删除的修订。
func (s *Service) Delete(ctx context.Context, id string, revision int64) error {
	select {
	case s.buildSlot <- struct{}{}:
		defer func() { <-s.buildSlot }()
	default:
		return fmt.Errorf("构建或评测期间不能删除数据集")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		return ErrNotFound
	}
	if d.Revision != revision {
		return fmt.Errorf("数据集修订已变化，请刷新后再删除")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM semantic_indexes WHERE substr(id,1,?)=?`, len(id)+1, id+"/"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM semantic_datasets WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	delete(s.datasets, id)
	for key, index := range s.indexes {
		if index.DatasetID == id {
			delete(s.indexes, key)
		}
	}
	return nil
}

func (s *Service) DeleteIndex(ctx context.Context, id string, expected IndexStatus) error {
	select {
	case s.buildSlot <- struct{}{}:
		defer func() { <-s.buildSlot }()
	default:
		return fmt.Errorf("构建或评测期间不能删除索引")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := id + "/" + profileKey(expected.Profile)
	var data []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM semantic_indexes WHERE id=?`, key).Scan(&data); err != nil {
		return fmt.Errorf("索引不存在或无法读取: %w", err)
	}
	var current IndexStatus
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.Revision != expected.Revision || !current.BuiltAt.Equal(expected.BuiltAt) {
		return fmt.Errorf("索引已重新发布，请刷新后再删除")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM semantic_indexes WHERE id=?`, key); err != nil {
		return err
	}
	delete(s.indexes, key)
	return nil
}

func (s *Service) validateKind(id, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if d.Kind != kind {
		return fmt.Errorf("数据集 %s 的用途不是 %s", id, kind)
	}
	return nil
}

func transactionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}
