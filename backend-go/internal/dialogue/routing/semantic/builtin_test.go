package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
	"github.com/stretchr/testify/require"
)

func TestBuiltinDatasetsAvailableWithoutImportAndDeletionPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "datasets.db")
	embedder := &testEmbedder{}
	s, err := New(path, embedder)
	require.NoError(t, err)
	items, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, source := range BuiltinDatasets() {
		d, err := s.Get(source.ID)
		require.NoError(t, err)
		source.Revision = 1
		require.Equal(t, source, d)
	}
	for _, item := range items {
		require.Zero(t, item.Approved)
		require.Empty(t, item.Indexes)
	}
	intents, err := s.Get(RecommendedIntentDatasetID)
	require.NoError(t, err)
	intents.Name = "我的意图样本"
	intents.Samples[0].Approved = true
	intents, err = s.Save(ctx, intents)
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, "starter-tools-v2", 1))
	require.NoError(t, s.Close())

	s, err = New(path, embedder)
	require.NoError(t, err)
	defer s.Close()
	actual, err := s.Get(intents.ID)
	require.NoError(t, err)
	require.Equal(t, intents, actual)
	_, err = s.Get("starter-tools-v2")
	require.ErrorIs(t, err, ErrNotFound)
	items, err = s.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Empty(t, embedder.calls, "初始化只保存原始样本，不调用向量模型")
}

func TestBuiltinInitializationPreservesPreviouslyImportedDataset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "datasets.db")
	db, err := sqlitedb.Open(path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE semantic_datasets(id TEXT PRIMARY KEY, data BLOB NOT NULL)`)
	require.NoError(t, err)
	d := BuiltinDatasets()[0]
	d.Name = "已导入并审核的样本"
	d.Revision = 7
	d.Samples[0].Approved = true
	data, err := json.Marshal(d)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO semantic_datasets(id,data) VALUES(?,?)`, d.ID, data)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s, err := New(path, &testEmbedder{})
	require.NoError(t, err)
	defer s.Close()
	actual, err := s.Get(d.ID)
	require.NoError(t, err)
	require.Equal(t, d, actual, "升级时不能覆盖同 ID 的已有修订与批准状态")
	_, err = s.Get("starter-tools-v2")
	require.NoError(t, err)
}

func TestBuiltinInitializationRollsBackWhenQuotaIsExceeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "datasets.db")
	db, err := sqlitedb.Open(path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE semantic_datasets(id TEXT PRIMARY KEY, data BLOB NOT NULL)`)
	require.NoError(t, err)
	for i := 0; i < 31; i++ {
		d := testDataset()
		d.ID = fmt.Sprintf("custom-%d", i)
		d.Revision = 1
		data, err := json.Marshal(d)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO semantic_datasets(id,data) VALUES(?,?)`, d.ID, data)
		require.NoError(t, err)
	}
	s, err := New(path, &testEmbedder{})
	if s != nil {
		t.Cleanup(func() { require.NoError(t, s.Close()) })
	}
	require.ErrorContains(t, err, "32 个数据集上限")
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM semantic_datasets`).Scan(&count))
	require.Equal(t, 31, count, "失败时回滚全部初始化写入")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM semantic_builtin_initializations`).Scan(&count))
	require.Zero(t, count)
}

func TestBuiltinUpgradeDoesNotBlockFullExistingLibrary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "datasets.db")
	s, err := New(path, &testEmbedder{})
	require.NoError(t, err)
	ctx := context.Background()
	// 模拟旧版本已有 v2 和安全模板，并已用完 32 个配额。
	_, err = s.db.Exec(`DELETE FROM semantic_datasets WHERE id=?`, RecommendedIntentDatasetID)
	require.NoError(t, err)
	_, err = s.db.Exec(`DELETE FROM semantic_builtin_initializations WHERE id=?`, RecommendedIntentDatasetID)
	require.NoError(t, err)
	for i := 0; i < 31; i++ {
		d := testDataset()
		d.ID = fmt.Sprintf("old-%d", i)
		if i == 0 {
			d.ID = "starter-intents-v2"
		}
		d.Revision = 1
		data, err := json.Marshal(d)
		require.NoError(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO semantic_datasets(id,data) VALUES(?,?)`, d.ID, data)
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())
	s, err = New(path, &testEmbedder{})
	require.NoError(t, err)
	defer s.Close()
	items, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 32)
	_, err = s.Get("starter-intents-v2")
	require.NoError(t, err)
	_, err = s.Get(RecommendedIntentDatasetID)
	require.ErrorIs(t, err, ErrNotFound)
}
