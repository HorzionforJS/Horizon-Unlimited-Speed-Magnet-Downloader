package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	// issuer 是签发者标识。签发和校验必须用同一个值，
	// 否则同密钥的其它系统签出的 token 会被误认为有效。
	issuer = "horizon"
	// accessTTL 是 access token 有效期。
	//
	// 15 分钟对「客户端只拿它调接口」太短了：用户点了下载、过一会儿再取回文件
	// 就会撞 401，而客户端并没有自动续期能力。这里放宽到 12 小时，
	// 换来的是被盗 token 的有效窗口变长——所以它必须配合 HTTPS 使用，
	// 且服务端要能改密钥让全部 token 立刻失效。
	accessTTL = 12 * time.Hour
	// refreshTTL 是 refresh token 有效期。
	refreshTTL = 30 * 24 * time.Hour
)

// Manager 负责密码哈希与 JWT 签发/校验。
//
// secrets 是密钥列表：第一个用于签发，全部用于校验。
// 这样轮换密钥时可以「新密钥签发 + 新旧都能验」，不必把所有用户踢下线。
type Manager struct {
	secrets [][]byte
}

// AccessTTL 返回 access token 有效期，供接口层告知客户端何时续期。
func AccessTTL() time.Duration { return accessTTL }

func New(secret string) *Manager {
	return NewWithSecrets([]string{secret})
}

// NewWithSecrets 用多密钥构造 Manager；至少提供一个非空密钥。
func NewWithSecrets(secrets []string) *Manager {
	out := &Manager{}
	for _, s := range secrets {
		if strings.TrimSpace(s) == "" {
			continue
		}
		out.secrets = append(out.secrets, []byte(s))
	}
	return out
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"uname"`
	IsAdmin  bool   `json:"adm"`
	// TokenType 区分 access 与 refresh，避免拿 refresh token 直接调业务接口。
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

func (m *Manager) sign(userID int64, username string, isAdmin bool, typ string, ttl time.Duration) (string, error) {
	if len(m.secrets) == 0 {
		return "", errors.New("未配置 JWT 密钥")
	}
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Username:  username,
		IsAdmin:   isAdmin,
		TokenType: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			// jti 必须存在：JWT 的其它字段精度只到秒，
			// 同一秒内签发的同一用户令牌会逐字节相同。
			// 这会让「续期后换发新令牌」退化成拿到同一把令牌，
			// 也让「刷新令牌轮换」失去意义（重放窗口不收敛）。
			ID:        newJTI(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secrets[0])
}

// newJTI 生成 128 位随机令牌 ID。
//
// 用 crypto/rand 而非时间戳或递增计数：前者不泄露签发顺序，
// 也不会在并发签发时产生碰撞。
func newJTI() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 熵源不可用时退回时间戳：仍能保证唯一性，只是可预测。
		return "t" + time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// Issue 签发 access token。
func (m *Manager) Issue(userID int64, username string, isAdmin bool) (string, error) {
	return m.sign(userID, username, isAdmin, TokenTypeAccess, accessTTL)
}

// IssuePair 同时签发 access 与 refresh token。
func (m *Manager) IssuePair(userID int64, username string, isAdmin bool) (access, refresh string, err error) {
	access, err = m.sign(userID, username, isAdmin, TokenTypeAccess, accessTTL)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.sign(userID, username, isAdmin, TokenTypeRefresh, refreshTTL)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func (m *Manager) parse(tokenStr string) (*Claims, error) {
	if len(m.secrets) == 0 {
		return nil, errors.New("未配置 JWT 密钥")
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		// 返回密钥集合：库会逐个尝试，因此新旧密钥可以同时有效，支持平滑轮换。
		keys := make([]jwt.VerificationKey, 0, len(m.secrets))
		for _, k := range m.secrets {
			keys = append(keys, k)
		}
		return jwt.VerificationKeySet{Keys: keys}, nil
	},
		jwt.WithIssuer(issuer),
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Parse 解析并校验 access token。
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	claims, err := m.parse(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "" && claims.TokenType != TokenTypeAccess {
		return nil, errors.New("token 类型错误：此处需要 access token")
	}
	return claims, nil
}

// ParseRefresh 解析并校验 refresh token。
func (m *Manager) ParseRefresh(tokenStr string) (*Claims, error) {
	claims, err := m.parse(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeRefresh {
		return nil, errors.New("token 类型错误：此处需要 refresh token")
	}
	return claims, nil
}

// Middleware 校验 Authorization: Bearer <token>。
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少 Bearer Token"})
			return
		}
		claims, err := m.Parse(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token 无效或已过期"})
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}

func ClaimsFrom(c *gin.Context) *Claims {
	v, _ := c.Get("claims")
	cl, _ := v.(*Claims)
	return cl
}
