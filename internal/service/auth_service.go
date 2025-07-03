// Package service 提供系统业务逻辑的实现
// 文件位置: internal/service/auth_service.go
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

// hmacKey 表示 JWT 使用的 HMAC 签名密钥
// 可通过环境变量 AEGIS_JWT_KEY 进行覆盖
var hmacKey = []byte("ArchiveAegisSecret_Hellohistory")

// ErrInvalidToken 表示解析出的 JWT 无效或已过期
var ErrInvalidToken = errors.New("invalid or expired token")

// init 初始化 JWT 密钥，支持从环境变量加载
func init() {
	envKey := os.Getenv("AEGIS_JWT_KEY")
	if envKey != "" {
		hmacKey = []byte(envKey)
		log.Println("信息: 使用环境变量 AEGIS_JWT_KEY 设置 JWT 密钥")
	} else {
		log.Println("警告: 未设置 AEGIS_JWT_KEY，使用默认 JWT 密钥。建议设置环境变量以提高安全性")
	}
}

// Claim 表示 JWT 中携带的用户信息
// 包括用户 ID、角色以及标准 JWT 注册字段
type Claim struct {
	ID   int64  `json:"id"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// UserCount 返回用户表中的总记录数
func UserCount(db *sql.DB) int {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM _user`).Scan(&n)
	if err != nil {
		log.Printf("错误: UserCount 查询失败: %v", err)
		return 0
	}
	return n
}

// CreateAdmin 创建一个具有管理员权限的用户
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

// CreateServiceAccount 创建一个服务账户，该账户用于服务间通信
func CreateServiceAccount(db *sql.DB, username string) (id int64, role string, err error) {
	role = "admin"
	_, err = db.Exec(`INSERT INTO _user(username, password_hash, role) VALUES (?, 'N/A', ?)`, username, role)
	if err != nil {
		return 0, "", fmt.Errorf("插入服务账户 '%s' 失败: %w", username, err)
	}

	id, _, ok := GetUserByUsername(db, username)
	if !ok {
		return 0, "", fmt.Errorf("创建后无法立即找到服务账户 '%s'", username)
	}

	log.Printf("信息: 已在数据库中成功创建服务账户 '%s' (ID: %d)", username, id)
	return id, role, nil
}

// CheckUser 校验用户名与密码是否匹配
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
	if hash == "N/A" {
		return 0, "", false
	}
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass))
	return id, role, err == nil
}

// GetUserById 根据用户 ID 查询用户名与角色信息
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

// GetUserByUsername 根据用户名查询用户 ID 与角色信息
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

// GenToken 为用户生成短期有效的 JWT
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

// GenServiceToken 为服务账户生成长期有效的 JWT
func GenServiceToken(uid int64, role string) (string, error) {
	claims := Claim{
		ID:   uid,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * 365 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "ArchiveAegis-Service",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(hmacKey)
}

// ParseToken 解析并验证 JWT 字符串的有效性
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

// CtxKey 表示用于 Context 的 Key 类型
// 定义为自定义类型以避免键名冲突
type CtxKey string

// ClaimKey 是存储在 Context 中的 Claim 对象的键
const ClaimKey CtxKey = "aegis-user-claim"

// ClaimFrom 从请求中提取 Claim 对象
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

// Authenticator 是用于实现认证中间件的结构体
// 持有数据库连接
type Authenticator struct {
	DB *sql.DB
}

// NewAuthenticator 创建新的 Authenticator 实例
func NewAuthenticator(db *sql.DB) *Authenticator {
	if db == nil {
		log.Fatal("严重错误: NewAuthenticator 接收到空的数据库连接！")
	}
	return &Authenticator{DB: db}
}

// Middleware 实现 JWT 验证中间件
// 验证通过后将用户信息注入到请求 context 中
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString != "" {
				claims, err := ParseToken(tokenString)
				if err == nil && claims != nil {
					_, _, userExists := GetUserById(a.DB, claims.ID)
					if userExists {
						ctx := context.WithValue(r.Context(), ClaimKey, claims)
						r = r.WithContext(ctx)
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
