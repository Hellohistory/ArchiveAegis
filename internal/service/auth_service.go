// Package aegauth — 用户表 + JWT 鉴权 + Middleware
package aegauth

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

/* ---------- 配置 ---------- */

var hmacKey = []byte("ArchiveAegisSecret_Hellohistory")

func init() {
	// 允许通过环境变量覆盖 JWT 密钥，增强安全性
	envKey := os.Getenv("AEGIS_JWT_KEY")
	if envKey != "" {
		hmacKey = []byte(envKey)
		log.Println("信息: aegauth 使用环境变量 AEGIS_JWT_KEY 设置的JWT密钥。")
	} else {
		log.Println("警告: aegauth 未找到环境变量 AEGIS_JWT_KEY，将使用默认JWT密钥。强烈建议设置环境变量以增强安全性！")
	}
}

/* ---------- DB schema and operations ---------- */

// InitUserTable 初始化用户表 (如果不存在)
func InitUserTable(db *sql.DB) error {
	_, err := db.Exec(`
       CREATE TABLE IF NOT EXISTS _user(
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          username TEXT UNIQUE NOT NULL,
          password_hash TEXT NOT NULL,
          role TEXT NOT NULL,
          
          rate_limit_per_second REAL,
          burst_size INTEGER
       );
    `)
	if err != nil {
		return fmt.Errorf("创建 _user 表失败: %w", err)
	}
	// 考虑为 username 创建索引以提高查询效率 (如果并发写入不多)
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_username ON _user (username);`)
	if err != nil {
		log.Printf("警告: 为 _user 表创建 username 索引失败 (可能已存在或DB不支持): %v", err)
		// 通常这不是一个致命错误，可以继续
	}
	return nil
}

// UserCount 返回用户表中的用户数量
func UserCount(db *sql.DB) int {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM _user`).Scan(&n)
	if err != nil {
		log.Printf("错误: UserCount 查询失败: %v", err)
		return 0 // 或返回 -1 表示错误，让调用方判断
	}
	return n
}

// CreateAdmin 创建一个管理员用户
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
		return fmt.Errorf("插入管理员用户 '%s' 失败: %w", user, err)
	}
	return nil
}

// CheckUser 校验用户名和密码，成功则返回用户 ID、角色和 true
func CheckUser(db *sql.DB, user, pass string) (id int64, role string, ok bool) {
	var hash string
	err := db.QueryRow(`SELECT id, password_hash, role FROM _user WHERE username = ?`, user).
		Scan(&id, &hash, &role)
	if err != nil {
		if !errors.Is(sql.ErrNoRows, err) {
			log.Printf("错误: CheckUser 查询用户 '%s' 时失败: %v", user, err)
		}
		return 0, "", false
	}
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass))
	return id, role, err == nil
}

// GetUserById 检索给定用户ID的用户名和角色
// 返回用户名、角色，如果找到则返回true，否则返回空字符串和false
func GetUserById(db *sql.DB, id int64) (username string, role string, ok bool) {
	err := db.QueryRow(`SELECT username, role FROM _user WHERE id = ?`, id).
		Scan(&username, &role)
	if err != nil {
		if !errors.Is(sql.ErrNoRows, err) {
			log.Printf("错误: GetUserById 查询用户 ID %d 时失败: %v", id, err)
		}
		return "", "", false
	}
	return username, role, true
}

/* ---------- JWT Handling ---------- */

// Claim 定义 JWT 的载荷结构
type Claim struct {
	ID   int64  `json:"id"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// GenToken 生成一个新的 JWT (有效期24小时)
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
	signedToken, err := token.SignedString(hmacKey)
	if err != nil {
		return "", fmt.Errorf("签名 JWT 失败: %w", err)
	}
	return signedToken, nil
}

// ErrInvalidToken 表示 JWT 无效、过期或解析失败。
var ErrInvalidToken = errors.New("invalid or expired token")

// ParseToken 解析并验证 JWT 字符串
func ParseToken(tokenString string) (*Claim, error) {
	claims := &Claim{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名方法: %v", token.Header["alg"])
		}
		return hmacKey, nil
	})

	if err != nil {
		// 特别处理过期错误，使其能被外部识别
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidToken, jwt.ErrTokenExpired)
		}
		return nil, fmt.Errorf("%w (detail: %v)", ErrInvalidToken, err)
	}

	if !token.Valid {
		return nil, ErrInvalidToken // 如果 token.Valid 是 false 但 err 是 nil (理论上不常见)
	}
	return claims, nil
}

/* ---------- Context Helpers for Claims ---------- */

type ctxKey int

const claimKey ctxKey = 0

func contextWithClaim(ctx context.Context, c *Claim) context.Context {
	return context.WithValue(ctx, claimKey, c)
}

func ClaimFrom(r *http.Request) *Claim {
	val := r.Context().Value(claimKey)
	if val == nil {
		return nil
	}
	claims, ok := val.(*Claim)
	if !ok {
		log.Printf("警告: context 中 claimKey 的值类型不是 *Claim: %T", val)
		return nil
	}
	return claims
}

/* ---------- 中间件 (Middleware) ---------- */

// Authenticator 结构体，用于持有数据库连接等依赖
type Authenticator struct {
	DB *sql.DB
}

// NewAuthenticator 创建 Authenticator 实例
func NewAuthenticator(db *sql.DB) *Authenticator {
	if db == nil {
		log.Fatal("严重错误: NewAuthenticator 接收到空的数据库连接！") // 或者返回错误
	}
	return &Authenticator{DB: db}
}

// Middleware 是一个JWT认证中间件。
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
						r = r.WithContext(contextWithClaim(r.Context(), claims))
					} else {
						log.Printf("认证中间件: 用户 ID %d (来自有效JWT) 在数据库中未找到。Token被拒绝。请求路径: %s, IP: %s", claims.ID, r.URL.Path, r.RemoteAddr)
					}
				} else {
					errMsg := "认证中间件: Token无效或解析错误。"
					if errors.Is(err, jwt.ErrTokenExpired) {
						errMsg = "认证中间件: Token已过期。"
					}
					log.Printf("%s 请求路径: %s, IP: %s (错误详情: %v)", errMsg, r.URL.Path, r.RemoteAddr, err)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
