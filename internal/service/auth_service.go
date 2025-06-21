// file: internal/service/auth_service.go
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
   常量与全局变量
============================================================================= */

// JWT HMAC 密钥（可通过环境变量 AEGIS_JWT_KEY 覆盖）
var hmacKey = []byte("ArchiveAegisSecret_Hellohistory")

// ErrInvalidToken 表示 JWT 无效或过期
var ErrInvalidToken = errors.New("invalid or expired token")

func init() {
	envKey := os.Getenv("AEGIS_JWT_KEY")
	if envKey != "" {
		hmacKey = []byte(envKey)
		log.Println("信息: 使用环境变量 AEGIS_JWT_KEY 设置 JWT 密钥")
	} else {
		log.Println("警告: 未设置 AEGIS_JWT_KEY，使用默认 JWT 密钥。建议设置环境变量以提高安全性")
	}
}

/* =============================================================================
   核心用户与认证逻辑
============================================================================= */

// Claim 定义了 JWT 中存储的用户信息 payload
type Claim struct {
	ID   int64  `json:"id"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// UserCount 返回数据库中的用户总数
func UserCount(db *sql.DB) int {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM _user`).Scan(&n)
	if err != nil {
		log.Printf("错误: UserCount 查询失败: %v", err)
		return 0
	}
	return n
}

// CreateAdmin 创建一个拥有管理员权限的普通用户账户
func CreateAdmin(db *sql.DB, user, pass string) error {
	if user == "" || pass == "" {
		return errors.New("用户名或密码不能为空")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}
	_, err = db.Exec(`INSERT INTO _user(username, password_hash, role) VALUES (?, ?, ?)`, user, string(hash), "admin")
	if err != nil {
		return fmt.Errorf("插入管理员用户失败: %w", err)
	}
	return nil
}

// CreateServiceAccount 在数据库中创建一个服务账户。
// 这类账户有特定的命名约定，且没有可用的密码，仅用于机器间认证。
func CreateServiceAccount(db *sql.DB, username string) (id int64, role string, err error) {
	// 服务账户统一给予 'admin' 角色，以便它们有足够权限，例如访问 /metrics
	// 密码哈希设为 'N/A'，因为它不可用于登录。
	role = "admin"
	_, err = db.Exec(`INSERT INTO _user(username, password_hash, role) VALUES (?, 'N/A', ?)`, username, role)
	if err != nil {
		return 0, "", fmt.Errorf("插入服务账户 '%s' 失败: %w", username, err)
	}

	// 获取刚刚插入的用户的ID
	id, _, ok := GetUserByUsername(db, username)
	if !ok {
		return 0, "", fmt.Errorf("创建后无法立即找到服务账户 '%s'", username)
	}

	log.Printf("信息: 已在数据库中成功创建服务账户 '%s' (ID: %d)", username, id)
	return id, role, nil
}

// CheckUser 校验普通用户的用户名和密码是否匹配
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
	if hash == "N/A" { // 服务账户不能通过密码登录
		return 0, "", false
	}
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass))
	return id, role, err == nil
}

// GetUserById 根据用户ID获取用户信息
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

// GetUserByUsername 根据用户名获取用户信息，主要用于服务账户的查找
func GetUserByUsername(db *sql.DB, username string) (id int64, role string, ok bool) {
	err := db.QueryRow(`SELECT id, role FROM _user WHERE username = ?`, username).
		Scan(&id, &role)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("错误: 查询用户 '%s' 失败: %v", username, err)
		}
		return 0, "", false
	}
	return id, role, true
}

// GenToken 为普通用户生成一个新的、有生命周期限制的 JWT
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

// GenServiceToken 为服务账户生成一个长生命周期的服务 Token
func GenServiceToken(uid int64, role string) (string, error) {
	claims := Claim{
		ID:   uid,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			// 设置一个非常长的过期时间，例如 10 年
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * 365 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "ArchiveAegis-Service", // 使用不同的发行方以作区分
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(hmacKey)
}

// ParseToken 解析 JWT 字符串，验证其签名和时效性
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

/* =============================================================================
   Context 上下文管理
============================================================================= */

// CtxKey 是用于 context 的 key 的类型。定义为特定类型以避免键冲突。
type CtxKey string

// ClaimKey 是用于在 context 中存储和检索用户 Claim 的唯一键。
// 导出此常量以确保整个应用程序（包括测试）都使用同一个键。
const ClaimKey CtxKey = "aegis-user-claim"

// ClaimFrom 从请求的 context 中提取用户 Claim
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
   HTTP 中间件
============================================================================= */

// Authenticator 是一个持有数据库连接的结构体，用于实现认证中间件
type Authenticator struct {
	DB *sql.DB
}

// NewAuthenticator 创建一个新的 Authenticator 实例
func NewAuthenticator(db *sql.DB) *Authenticator {
	if db == nil {
		log.Fatal("严重错误: NewAuthenticator 接收到空的数据库连接！")
	}
	return &Authenticator{DB: db}
}

// Middleware 是 JWT 中间件：验证 Token 并将用户信息（Claim）注入到请求的 context 中
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString != "" {
				claims, err := ParseToken(tokenString)
				if err == nil && claims != nil {
					// 令牌有效，再确认一下用户是否仍然存在于数据库中
					_, _, userExists := GetUserById(a.DB, claims.ID)
					if userExists {
						// 用户存在，将 claim 注入 context
						ctx := context.WithValue(r.Context(), ClaimKey, claims)
						r = r.WithContext(ctx)
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
