// Package store 提供 SQLite 持久层，存放会增长的运行时数据
// （备份历史、下载任务等）。配置仍走 YAML，两者职责分开。
// 使用 modernc.org/sqlite（纯 Go，无 CGO，兼容多架构静态构建）。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store SQLite 数据库。
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// New 打开/创建数据库（路径为 dataDir/ddnas.db）。
func New(dataDir string) (*Store, error) {
	dsn := filepath.Join(dataDir, "ddnas.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite 单写者模式，避免并发写冲突
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS backup_history (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	ts          INTEGER NOT NULL,           -- epoch 毫秒
	duration_ms INTEGER NOT NULL DEFAULT 0,
	total       INTEGER NOT NULL DEFAULT 0,
	success     INTEGER NOT NULL DEFAULT 0,
	failed      INTEGER NOT NULL DEFAULT 0,
	failed_list TEXT    NOT NULL DEFAULT '[]',  -- JSON 数组 ["a.jpg","b.mp4"]
	tree_hash   TEXT    NOT NULL DEFAULT '',   -- treeUri hashCode，区分来源
	remote_base TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_backup_ts ON backup_history(ts DESC);

CREATE TABLE IF NOT EXISTS download_tasks (
	id          TEXT PRIMARY KEY,             -- 上游任务 ID
	name        TEXT NOT NULL DEFAULT '',
	url         TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'queued', -- queued/downloading/paused/done/error
	progress    REAL    NOT NULL DEFAULT 0,    -- 0~1
	speed       INTEGER NOT NULL DEFAULT 0,    -- bytes/s
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL,
	adapter     TEXT NOT NULL DEFAULT ''       -- aria2/qbittorrent/...
);
CREATE INDEX IF NOT EXISTS idx_dl_status ON download_tasks(status);
`)
	return err
}

// --- 备份历史 ---

// BackupRecord 一条备份历史。
type BackupRecord struct {
	ID         int64    `json:"id"`
	Ts         int64    `json:"ts"`          // epoch 毫秒
	DurationMs int64    `json:"duration_ms"`
	Total      int      `json:"total"`
	Success    int      `json:"success"`
	Failed     int      `json:"failed"`
	FailedList []string `json:"failed_list"` // 失败文件名列表
	TreeHash   string   `json:"tree_hash"`
	RemoteBase string   `json:"remote_base"`
}

// InsertBackupRecord 插入一条备份历史。
func (s *Store) InsertBackupRecord(r BackupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fl, _ := json.Marshal(r.FailedList)
	_, err := s.db.Exec(
		`INSERT INTO backup_history (ts,duration_ms,total,success,failed,failed_list,tree_hash,remote_base) VALUES (?,?,?,?,?,?,?,?)`,
		r.Ts, r.DurationMs, r.Total, r.Success, r.Failed, string(fl), r.TreeHash, r.RemoteBase,
	)
	return err
}

// ListBackupHistory 返回最近 N 条备份历史（默认 20）。
func (s *Store) ListBackupHistory(limit int) ([]BackupRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id,ts,duration_ms,total,success,failed,failed_list,tree_hash,remote_base FROM backup_history ORDER BY ts DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRecord
	for rows.Next() {
		var r BackupRecord
		var fl string
		if err := rows.Scan(&r.ID, &r.Ts, &r.DurationMs, &r.Total, &r.Success, &r.Failed, &fl, &r.TreeHash, &r.RemoteBase); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fl), &r.FailedList)
		out = append(out, r)
	}
	return out, nil
}

// --- 下载任务 ---

// DownloadTask 一条下载任务快照。
type DownloadTask struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	URL       string  `json:"url"`
	Status    string  `json:"status"`
	Progress  float64 `json:"progress"`
	Speed     int64   `json:"speed"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
	Adapter   string  `json:"adapter"`
}

// UpsertDownloadTask 插入或更新一条下载任务。
func (s *Store) UpsertDownloadTask(t DownloadTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := s.db.Exec(
		`INSERT INTO download_tasks (id,name,url,status,progress,speed,created_at,updated_at,adapter)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name,status=excluded.status,
		   progress=excluded.progress,speed=excluded.speed,updated_at=excluded.updated_at`,
		t.ID, t.Name, t.URL, t.Status, t.Progress, t.Speed, t.CreatedAt, t.UpdatedAt, t.Adapter,
	)
	return err
}

// ListDownloadTasks 返回所有下载任务（按状态排序）。
func (s *Store) ListDownloadTasks() ([]DownloadTask, error) {
	rows, err := s.db.Query(
		`SELECT id,name,url,status,progress,speed,created_at,updated_at,adapter FROM download_tasks ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DownloadTask
	for rows.Next() {
		var t DownloadTask
		if err := rows.Scan(&t.ID, &t.Name, &t.URL, &t.Status, &t.Progress, &t.Speed, &t.CreatedAt, &t.UpdatedAt, &t.Adapter); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// DeleteDownloadTask 删除一条下载任务。
func (s *Store) DeleteDownloadTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM download_tasks WHERE id=?`, id)
	return err
}
