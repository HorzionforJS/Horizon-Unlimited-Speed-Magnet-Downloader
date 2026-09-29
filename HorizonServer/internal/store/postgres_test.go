package store

import (
	"os"
	"testing"
)

// TestPostgresIntegration 在设置了 HORIZON_TEST_POSTGRES_DSN 时运行，
// 验证 PostgreSQL 存储实现的建表/用户/黑名单/审计全链路。未设置则跳过。
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("HORIZON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("未设置 HORIZON_TEST_POSTGRES_DSN，跳过 PostgreSQL 集成测试")
	}
	s, err := NewPostgres(dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	defer s.Close()

	u := &User{Username: "tuser", PassHash: "hash", IsAdmin: true}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("CreateUser 未回填 ID")
	}
	got, err := s.GetUserByUsername("tuser")
	if err != nil || got == nil || !got.IsAdmin {
		t.Fatalf("GetUserByUsername: got=%+v err=%v", got, err)
	}

	const h = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := s.AddBlockedHash(h, "test"); err != nil {
		t.Fatalf("AddBlockedHash: %v", err)
	}
	if blocked, _ := s.IsHashBlocked(h); !blocked {
		t.Fatal("IsHashBlocked 应为 true")
	}
	if err := s.AddBlockedKeyword("dmca"); err != nil {
		t.Fatalf("AddBlockedKeyword: %v", err)
	}
	if kw, hit, _ := s.MatchBlockedKeyword("xxx-DMCA-xxx"); !hit || kw != "dmca" {
		t.Fatalf("MatchBlockedKeyword: hit=%v kw=%q", hit, kw)
	}
	if err := s.Audit("t", "test", "x", ""); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	audit, _ := s.ListAudit(10)
	if len(audit) < 1 {
		t.Fatal("audit 应至少 1 条")
	}
}
