package intent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/insistanan/api-proxy/internal/sqlitedb"
)

type stateStore struct{ db *sql.DB }

// 每次事务仅更新发生变化的记录，避免每个流式请求重写全部会话文件。
func openStateStore(path string) (*stateStore, persistedState, error) {
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, persistedState{}, err
	}
	store := &stateStore{db: db}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS intent_state (kind TEXT NOT NULL, id TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(kind, id))`); err != nil {
		return nil, persistedState{}, errors.Join(err, db.Close())
	}
	state := persistedState{Sessions: make(map[string]sessionState), Quality: make(map[string]quality), Events: []Event{}}
	rows, err := db.Query(`SELECT kind, id, data FROM intent_state`)
	if err != nil {
		return nil, state, errors.Join(err, db.Close())
	}
	for rows.Next() {
		var kind, id string
		var data []byte
		if err = rows.Scan(&kind, &id, &data); err != nil {
			break
		}
		switch kind {
		case "session":
			var value sessionState
			if err = json.Unmarshal(data, &value); err == nil {
				state.Sessions[id] = value
			}
		case "quality":
			var value quality
			if err = json.Unmarshal(data, &value); err == nil {
				state.Quality[id] = value
			}
		case "event":
			var value Event
			if err = json.Unmarshal(data, &value); err == nil {
				state.Events = append(state.Events, value)
			}
		default:
			err = fmt.Errorf("不支持的意图存储记录类型 %q", kind)
		}
		if err != nil {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, state, errors.Join(err, db.Close())
	}
	sort.Slice(state.Events, func(i, j int) bool { return state.Events[i].At.Before(state.Events[j].At) })
	return store, state, nil
}

func (s *stateStore) save(previous, next persistedState) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, rollbackErr)
			}
		}
	}()
	put := func(kind, id string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO intent_state(kind,id,data) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, kind, id, data)
		return err
	}
	remove := func(kind, id string) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM intent_state WHERE kind=? AND id=?`, kind, id)
		return err
	}
	for id, value := range next.Sessions {
		if old, exists := previous.Sessions[id]; exists && old == value {
			continue
		}
		if err = put("session", id, value); err != nil {
			return err
		}
	}
	for id := range previous.Sessions {
		if _, exists := next.Sessions[id]; !exists {
			if err = remove("session", id); err != nil {
				return err
			}
		}
	}
	for id, value := range next.Quality {
		if old, exists := previous.Quality[id]; exists && old == value {
			continue
		}
		if err = put("quality", id, value); err != nil {
			return err
		}
	}
	previousEvents, nextEvents := make(map[string]Event), make(map[string]Event)
	for _, event := range previous.Events {
		previousEvents[event.RequestID] = event
	}
	for _, event := range next.Events {
		nextEvents[event.RequestID] = event
		if old, exists := previousEvents[event.RequestID]; exists && reflect.DeepEqual(old, event) {
			continue
		}
		if err = put("event", event.RequestID, event); err != nil {
			return err
		}
	}
	for id := range previousEvents {
		if _, exists := nextEvents[id]; !exists {
			if err = remove("event", id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
