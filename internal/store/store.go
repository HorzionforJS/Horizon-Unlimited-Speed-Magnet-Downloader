package store

import (
	"database/sql"
	"strings"
	"time"
)

type User struct {
	ID        int64
	Username  string
	PassHash  string
	IsAdmin   bool
	CreatedAt time.Time
}

type Task struct {
	ID        int64
	OwnerID   int64
	InfoHash  string
	Magnet    string
	Name      string
	Status    string // resolving / active / paused / complete / error / removed
	Dir       string
	Total     int64
	Completed int64
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// Backend 是存储层抽象：SQLite（默认）与 PostgreSQL 均实现该接口。
type Backend interface {
	CreateUser(u *User) error
	GetUserByUsername(name string) (*User, error)
	IsUsernameTaken(name string) (bool, error)
	UserCount() (int, error)
	CreateTask(t *Task) error
	ListTasks(ownerID int64) ([]*Task, error)
	GetTask(ownerID int64, infoHash string) (*Task, error)
	UpdateTaskProgress(ownerID int64, infoHash, name, status string, total, completed int64) error
	SetStatus(ownerID int64, infoHash, status string) error
	DeleteTask(ownerID int64, infoHash string) error
	ListAllTasks() ([]*Task, error)
	DeleteTaskByHash(infoHash string) error
	AddBlockedHash(infoHash, reason string) error
	RemoveBlockedHash(infoHash string) error
	IsHashBlocked(infoHash string) (bool, error)
	ListBlockedHashes() ([]BlockedHash, error)
	AddBlockedKeyword(keyword string) error
	RemoveBlockedKeyword(keyword string) error
	ListBlockedKeywords() ([]string, error)
	MatchBlockedKeyword(text string) (string, bool, error)
	Audit(actor, action, target, detail string) error
	ListAudit(limit int) ([]AuditEntry, error)
	Close() error
}

func New(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			pass_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id INTEGER NOT NULL,
			info_hash TEXT NOT NULL,
			magnet TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'resolving',
			dir TEXT NOT NULL DEFAULT '',
			total INTEGER NOT NULL DEFAULT 0,
			completed INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(owner_id, info_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks(owner_id)`,
		`CREATE TABLE IF NOT EXISTS blocked_hashes (
			info_hash TEXT PRIMARY KEY,
			reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS blocked_keywords (
			keyword TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateUser(u *User) error {
	res, err := s.db.Exec(`INSERT INTO users(username, pass_hash, is_admin) VALUES(?,?,?)`,
		u.Username, u.PassHash, boolInt(u.IsAdmin))
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetUserByUsername(name string) (*User, error) {
	var u User
	var adm int
	err := s.db.QueryRow(`SELECT id, username, pass_hash, is_admin, created_at FROM users WHERE username=?`, name).
		Scan(&u.ID, &u.Username, &u.PassHash, &adm, &u.CreatedAt)
	if err != nil {
		return nil, err // sql.ErrNoRows 表示不存在
	}
	u.IsAdmin = adm != 0
	return &u, nil
}

func (s *Store) IsUsernameTaken(name string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE username=?`, name).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// UserCount 返回用户总数（用于首个注册用户提升为管理员）。
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateTask(t *Task) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO tasks(owner_id, info_hash, magnet, name, status, dir, total, completed)
		VALUES(?,?,?,?,?,?,?,?)`,
		t.OwnerID, t.InfoHash, t.Magnet, t.Name, t.Status, t.Dir, t.Total, t.Completed)
	return err
}

func (s *Store) ListTasks(ownerID int64) ([]*Task, error) {
	rows, err := s.db.Query(`SELECT id, owner_id, info_hash, magnet, name, status, dir, total, completed, created_at
		FROM tasks WHERE owner_id=? ORDER BY id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Magnet, &t.Name, &t.Status, &t.Dir, &t.Total, &t.Completed, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *Store) GetTask(ownerID int64, infoHash string) (*Task, error) {
	var t Task
	err := s.db.QueryRow(`SELECT id, owner_id, info_hash, magnet, name, status, dir, total, completed, created_at
		FROM tasks WHERE owner_id=? AND info_hash=?`, ownerID, infoHash).
		Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Magnet, &t.Name, &t.Status, &t.Dir, &t.Total, &t.Completed, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) UpdateTaskProgress(ownerID int64, infoHash, name, status string, total, completed int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET name=?, status=?, total=?, completed=? WHERE owner_id=? AND info_hash=?`,
		name, status, total, completed, ownerID, infoHash)
	return err
}

func (s *Store) SetStatus(ownerID int64, infoHash, status string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status=? WHERE owner_id=? AND info_hash=?`, status, ownerID, infoHash)
	return err
}

func (s *Store) DeleteTask(ownerID int64, infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE owner_id=? AND info_hash=?`, ownerID, infoHash)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ============ 黑名单 / 审计日志（安全合规） ============

type BlockedHash struct {
	InfoHash  string
	Reason    string
	CreatedAt time.Time
}

type AuditEntry struct {
	ID        int64
	Actor     string
	Action    string
	Target    string
	Detail    string
	CreatedAt time.Time
}

func (s *Store) ListAllTasks() ([]*Task, error) {
	rows, err := s.db.Query(`SELECT id, owner_id, info_hash, magnet, name, status, dir, total, completed, created_at
		FROM tasks ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Magnet, &t.Name, &t.Status, &t.Dir, &t.Total, &t.Completed, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTaskByHash(infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE info_hash=?`, infoHash)
	return err
}

func (s *Store) AddBlockedHash(infoHash, reason string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO blocked_hashes(info_hash, reason) VALUES(?,?)`, infoHash, reason)
	return err
}

func (s *Store) RemoveBlockedHash(infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM blocked_hashes WHERE info_hash=?`, infoHash)
	return err
}

func (s *Store) IsHashBlocked(infoHash string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM blocked_hashes WHERE info_hash=?`, infoHash).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListBlockedHashes() ([]BlockedHash, error) {
	rows, err := s.db.Query(`SELECT info_hash, reason, created_at FROM blocked_hashes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedHash
	for rows.Next() {
		var b BlockedHash
		if err := rows.Scan(&b.InfoHash, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) AddBlockedKeyword(keyword string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO blocked_keywords(keyword) VALUES(?)`, keyword)
	return err
}

func (s *Store) RemoveBlockedKeyword(keyword string) error {
	_, err := s.db.Exec(`DELETE FROM blocked_keywords WHERE keyword=?`, keyword)
	return err
}

func (s *Store) ListBlockedKeywords() ([]string, error) {
	rows, err := s.db.Query(`SELECT keyword FROM blocked_keywords ORDER BY keyword`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// MatchBlockedKeyword 对文本做不区分大小写的子串匹配，返回命中的关键词。
func (s *Store) MatchBlockedKeyword(text string) (string, bool, error) {
	kws, err := s.ListBlockedKeywords()
	if err != nil {
		return "", false, err
	}
	lower := strings.ToLower(text)
	for _, kw := range kws {
		if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
			return kw, true, nil
		}
	}
	return "", false, nil
}

func (s *Store) Audit(actor, action, target, detail string) error {
	_, err := s.db.Exec(`INSERT INTO audit_log(actor, action, target, detail) VALUES(?,?,?,?)`,
		actor, action, target, detail)
	return err
}

func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, actor, action, target, detail, created_at FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Target, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
