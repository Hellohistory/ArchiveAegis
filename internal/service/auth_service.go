// Package service — 用户表 + JWT 鉴权服务 + HTTP 中间件
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

/* =============================================================================
   服务层：用户表操作 + JWT 生成解析（无HTTP依赖）
============================================================================= */

// JWT HMAC 密钥（可通过环境变量 AEGIS_JWT_KEY 覆盖）
var hmacKey = []byte("ArchiveAegisSecret_Hellohistory")

func init() {
	envKey := os.Getenv("AEGIS_JWT_KEY")
	if envKey != "" {
		hmacKey = []byte(envKey)
		log.Println("信息: 使用环境变量 AEGIS_JWT_KEY 设置 JWT 密钥")
	} else {
		log.Println("警告: 未设置 AEGIS_JWT_KEY，使用默认 JWT 密钥。建议设置环境变量以提高安全性")
	}
}

// UserCount 返回用户数
func UserCount(db *sql.DB) int {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM _user`).Scan(&n)
	if err != nil {
		log.Printf("错误: UserCount 查询失败: %v", err)
		return 0
	}
	return n
}

// CreateAdmin 创建管理员账户
func CreateAdmin(db *sql.DB, user, pass string) error {
	if user == "" || pass == "" {
		return errors.New("用户名或密码不能为空")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}
	_, err = db.Exec(`
		INSERT INTO _user(username, password_hash, role)
		VALUES (?, ?, ?)`, user, string(hash), "admin")
	if err != nil {
		return fmt.Errorf("插入管理员用户失败: %w", err)
	}
	return nil
}

func CheckUser(db *sql.DB, user, pass string) (id int64, role string, ok bool) {
	var hash string
	err := db.QueryRow(`SELECT id, password_hash, role FROM _user WHERE username = ?`, user).
		Scan(&id, &hash, &role)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("错误: 查询用户 '%s' 失败: %v", user, err)
		}
		return 0, "", false
	}
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass))
	return id, role, err == nil
}

// GetUserById 根据 ID 获取用户信息
func GetUserById(db *sql.DB, id int64) (username string, role string, ok bool) {
	err := db.QueryRow(`SELECT username, role FROM _user WHERE id = ?`, id).
		Scan(&username, &role)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("错误: 查询用户 ID %d 失败: %v", id, err)
		}
		return "", "", false
	}
	return username, role, true
}

// Claim 定义 JWT payload
type Claim struct {
	ID   int64  `json:"id"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// GenToken 生成新的 JWT
func GenToken(uid int64, role string) (string, error) {
	claims := Claim{
		ID:   uid,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "ArchiveAegis",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(hmacKey)
}

// ErrInvalidToken 表示 JWT 无效或过期
var ErrInvalidToken = errors.New("invalid or expired token")

// ParseToken 解析 JWT 字符串
func ParseToken(tokenString string) (*Claim, error) {
	claims := &Claim{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期签名方法: %v", token.Header["alg"])
		}
		return hmacKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidToken, jwt.ErrTokenExpired)
		}
		return nil, fmt.Errorf("%w (detail: %v)", ErrInvalidToken, err)
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

/* ---------- Context 助手函数 ---------- */

// CtxKey 是用于 context 的 key 的类型，定义为 string 以避免跨包使用时的冲突。
// 将其导出（首字母大写），以便其他包（如测试包）可以安全地使用它。
type CtxKey string

// ClaimKey 是用于在 context 中存储和检索用户 Claim 的唯一键，将其导出为常量，确保整个应用程序都使用同一个键。
const ClaimKey CtxKey = "ArchiveAegis_Hellohistory"

// contextWithClaim 是一个内部辅助函数，用于将 Claim 添加到 context 中。
// 它现在使用导出的 ClaimKey。
func contextWithClaim(ctx context.Context, c *Claim) context.Context {
	return context.WithValue(ctx, ClaimKey, c)
}

// ClaimFrom 从请求的 context 中提取用户 Claim，它现在也使用导出的 ClaimKey。
func ClaimFrom(r *http.Request) *Claim {
	val := r.Context().Value(ClaimKey)
	if val == nil {
		return nil
	}
	claims, ok := val.(*Claim)
	if !ok {
		log.Printf("警告: context 中 ClaimKey 的值类型不是 *Claim: %T", val)
		return nil
	}
	return claims
}

/* =============================================================================
   HTTP 层: Authenticator 结构体与中间件
============================================================================= */

// Authenticator 是 HTTP 中间件用的结构，持有 DB
type Authenticator struct {
	DB *sql.DB
}

// NewAuthenticator 构造器
func NewAuthenticator(db *sql.DB) *Authenticator {
	if db == nil {
		log.Fatal("严重错误: NewAuthenticator 接收到空的数据库连接！")
	}
	return &Authenticator{DB: db}
}

// Middleware 是 JWT 中间件：验证 Token 并注入 Claim。
// 它现在通过调用 contextWithClaim 来使用正确的、导出的 context key。
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString != "" {
				claims, err := ParseToken(tokenString)

				if err == nil && claims != nil {
					// 确认用户仍然存在于数据库中
					_, _, userExists := GetUserById(a.DB, claims.ID)
					if userExists {
						// 使用辅助函数将 claim 注入 context
						r = r.WithContext(contextWithClaim(r.Context(), claims))
					} else {
						log.Printf("认证中间件: 用户 ID %d 不存在，拒绝请求. 路径: %s, IP: %s", claims.ID, r.URL.Path, r.RemoteAddr)
					}
				} else {
					errMsg := "认证中间件: Token 无效或解析失败"
					if errors.Is(err, jwt.ErrTokenExpired) {
						errMsg = "认证中间件: Token 已过期"
					}
					log.Printf("%s，请求路径: %s, IP: %s (详情: %v)", errMsg, r.URL.Path, r.RemoteAddr, err)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
