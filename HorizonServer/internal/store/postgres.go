package store

import (
	"database/sql"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore 是基于 PostgreSQL 的存储实现（生产环境可选，db-type=postgres）。
type PostgresStore struct {
	db *sql.DB
}

// NewPostgres 连接 PostgreSQL 并建表。dsn 形如
// postgres://user:pass@host:5432/horizon?sslmode=disable
func NewPostgres(dsn string) (Backend, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			pass_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id BIGSERIAL PRIMARY KEY,
			owner_id BIGINT NOT NULL,
			info_hash TEXT NOT NULL,
			magnet TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'resolving',
			dir TEXT NOT NULL DEFAULT '',
			total BIGINT NOT NULL DEFAULT 0,
			completed BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(owner_id, info_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks(owner_id)`,
		`CREATE TABLE IF NOT EXISTS blocked_hashes (
			info_hash TEXT PRIMARY KEY,
			reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS blocked_keywords (
			keyword TEXT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id BIGSERIAL PRIMARY KEY,
			actor TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) CreateUser(u *User) error {
	return s.db.QueryRow(`INSERT INTO users(username, pass_hash, is_admin) VALUES($1,$2,$3) RETURNING id`,
		u.Username, u.PassHash, boolInt(u.IsAdmin)).Scan(&u.ID)
}

func (s *PostgresStore) GetUserByUsername(name string) (*User, error) {
	var u User
	var adm int
	err := s.db.QueryRow(`SELECT id, username, pass_hash, is_admin, created_at FROM users WHERE username=$1`, name).
		Scan(&u.ID, &u.Username, &u.PassHash, &adm, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = adm != 0
	return &u, nil
}

func (s *PostgresStore) IsUsernameTaken(name string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE username=$1`, name).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *PostgresStore) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *PostgresStore) CreateTask(t *Task) error {
	_, err := s.db.Exec(`INSERT INTO tasks(owner_id, info_hash, magnet, name, status, dir, total, completed)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (owner_id, info_hash) DO NOTHING`,
		t.OwnerID, t.InfoHash, t.Magnet, t.Name, t.Status, t.Dir, t.Total, t.Completed)
	return err
}

func (s *PostgresStore) ListTasks(ownerID int64) ([]*Task, error) {
	rows, err := s.db.Query(`SELECT id, owner_id, info_hash, magnet, name, status, dir, total, completed, created_at
		FROM tasks WHERE owner_id=$1 ORDER BY id DESC`, ownerID)
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

func (s *PostgresStore) GetTask(ownerID int64, infoHash string) (*Task, error) {
	var t Task
	err := s.db.QueryRow(`SELECT id, owner_id, info_hash, magnet, name, status, dir, total, completed, created_at
		FROM tasks WHERE owner_id=$1 AND info_hash=$2`, ownerID, infoHash).
		Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Magnet, &t.Name, &t.Status, &t.Dir, &t.Total, &t.Completed, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *PostgresStore) UpdateTaskProgress(ownerID int64, infoHash, name, status string, total, completed int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET name=$1, status=$2, total=$3, completed=$4 WHERE owner_id=$5 AND info_hash=$6`,
		name, status, total, completed, ownerID, infoHash)
	return err
}

func (s *PostgresStore) SetStatus(ownerID int64, infoHash, status string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status=$1 WHERE owner_id=$2 AND info_hash=$3`, status, ownerID, infoHash)
	return err
}

func (s *PostgresStore) DeleteTask(ownerID int64, infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE owner_id=$1 AND info_hash=$2`, ownerID, infoHash)
	return err
}

func (s *PostgresStore) ListAllTasks() ([]*Task, error) {
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

func (s *PostgresStore) DeleteTaskByHash(infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE info_hash=$1`, infoHash)
	return err
}

func (s *PostgresStore) AddBlockedHash(infoHash, reason string) error {
	_, err := s.db.Exec(`INSERT INTO blocked_hashes(info_hash, reason) VALUES($1,$2)
		ON CONFLICT (info_hash) DO UPDATE SET reason=EXCLUDED.reason`, infoHash, reason)
	return err
}

func (s *PostgresStore) RemoveBlockedHash(infoHash string) error {
	_, err := s.db.Exec(`DELETE FROM blocked_hashes WHERE info_hash=$1`, infoHash)
	return err
}

func (s *PostgresStore) IsHashBlocked(infoHash string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM blocked_hashes WHERE info_hash=$1`, infoHash).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *PostgresStore) ListBlockedHashes() ([]BlockedHash, error) {
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

func (s *PostgresStore) AddBlockedKeyword(keyword string) error {
	_, err := s.db.Exec(`INSERT INTO blocked_keywords(keyword) VALUES($1) ON CONFLICT (keyword) DO NOTHING`, keyword)
	return err
}

func (s *PostgresStore) RemoveBlockedKeyword(keyword string) error {
	_, err := s.db.Exec(`DELETE FROM blocked_keywords WHERE keyword=$1`, keyword)
	return err
}

func (s *PostgresStore) ListBlockedKeywords() ([]string, error) {
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

func (s *PostgresStore) MatchBlockedKeyword(text string) (string, bool, error) {
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

func (s *PostgresStore) Audit(actor, action, target, detail string) error {
	_, err := s.db.Exec(`INSERT INTO audit_log(actor, action, target, detail) VALUES($1,$2,$3,$4)`,
		actor, action, target, detail)
	return err
}

func (s *PostgresStore) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, actor, action, target, detail, created_at FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
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
