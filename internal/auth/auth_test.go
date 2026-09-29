package auth

import (
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("test-password-not-a-real-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "test-password-not-a-real-secret") {
		t.Fatal("正确密码应通过校验")
	}
	if CheckPassword(h, "wrong") {
		t.Fatal("错误密码不应通过校验")
	}
}

func TestIssueAndParse(t *testing.T) {
	m := New("secret-1")
	tok, err := m.Issue(7, "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != 7 || c.Username != "alice" || !c.IsAdmin {
		t.Fatalf("claims 不符: %+v", c)
	}
	if c.TokenType != TokenTypeAccess {
		t.Fatalf("应为 access token，实际 %q", c.TokenType)
	}
}

// refresh token 不能当 access token 用。
func TestRefreshRejectedAsAccess(t *testing.T) {
	m := New("secret-1")
	_, refresh, err := m.IssuePair(1, "bob", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(refresh); err == nil {
		t.Fatal("refresh token 不应能通过 access 校验")
	}
	if _, err := m.ParseRefresh(refresh); err != nil {
		t.Fatalf("refresh token 应能通过 refresh 校验: %v", err)
	}
}

// 用别的密钥签的 token 必须被拒绝（Issuer/密钥都要校验）。
func TestWrongSecretRejected(t *testing.T) {
	a := New("secret-a")
	b := New("secret-b")
	tok, _ := a.Issue(1, "x", false)
	if _, err := b.Parse(tok); err == nil {
		t.Fatal("不同密钥签发的 token 不应通过")
	}
}

// 多密钥应支持平滑轮换：新密钥签发，旧密钥仍可验证。
func TestKeyRotation(t *testing.T) {
	old := New("old-secret")
	oldTok, _ := old.Issue(1, "x", false)

	rotated := NewWithSecrets([]string{"new-secret", "old-secret"})
	if _, err := rotated.Parse(oldTok); err != nil {
		t.Fatalf("轮换后旧 token 应仍有效: %v", err)
	}
	newTok, _ := rotated.Issue(2, "y", false)
	if _, err := rotated.Parse(newTok); err != nil {
		t.Fatalf("新 token 应有效: %v", err)
	}
	if _, err := old.Parse(newTok); err == nil {
		t.Fatal("旧实例不应能验证新密钥签发的 token")
	}
}

func TestExpiry(t *testing.T) {
	m := New("s")
	if _, err := m.Issue(1, "x", false); err != nil {
		t.Fatal(err)
	}
	if accessTTL <= 0 || refreshTTL <= accessTTL {
		t.Fatalf("TTL 配置不合理: access=%v refresh=%v", accessTTL, refreshTTL)
	}
	if issuer == "" {
		t.Fatal("issuer 不能为空")
	}
	_ = time.Now
}

// TestIssuePairIsAlwaysUnique 守住 jti：JWT 的时间字段只精确到秒，
// 若不带 jti，同一秒内为同一用户签发的令牌会完全相同——
// 这会让 refresh 轮换与 access 换发同时失去意义。
func TestIssuePairIsAlwaysUnique(t *testing.T) {
	m := New("secret-a")
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		access, refresh, err := m.IssuePair(7, "alice", false)
		if err != nil {
			t.Fatalf("IssuePair: %v", err)
		}
		if seen[access] {
			t.Fatalf("第 %d 次签发出的 access token 与之前完全相同（缺少 jti）", i)
		}
		if seen[refresh] {
			t.Fatalf("第 %d 次签发出的 refresh token 与之前完全相同（缺少 jti）", i)
		}
		seen[access] = true
		seen[refresh] = true
		if access == refresh {
			t.Fatal("access 与 refresh 不应相同")
		}
	}
}

// TestJTIChangesOnRefresh 明确表达「续期必须换到新令牌」这一契约，
// 客户端正是依赖它判断续期是否真的发生。
func TestJTIChangesOnRefresh(t *testing.T) {
	m := New("secret-a")
	_, refresh1, err := m.IssuePair(1, "root", true)
	if err != nil {
		t.Fatalf("IssuePair: %v", err)
	}
	claims, err := m.ParseRefresh(refresh1)
	if err != nil {
		t.Fatalf("ParseRefresh: %v", err)
	}
	if claims.ID == "" {
		t.Fatal("refresh token 缺少 jti")
	}
	access2, refresh2, err := m.IssuePair(claims.UserID, claims.Username, claims.IsAdmin)
	if err != nil {
		t.Fatalf("IssuePair 第二次: %v", err)
	}
	if refresh2 == refresh1 {
		t.Fatal("续期后 refresh token 未变化")
	}
	claims2, err := m.Parse(access2)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims2.ID == "" || claims2.ID == claims.ID {
		t.Fatal("新 access token 的 jti 未变化")
	}
}
