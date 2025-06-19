package aegmiddleware

import (
	"ArchiveAegis/internal/service"
	"log"
	"net/http"
)

// RequireAdmin 是一个确保只有管理员能访问的中间件。
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := service.ClaimFrom(r)
		if claims == nil {
			log.Printf("RequireAdmin: 访问被拒绝 (无有效Claim - Token可能缺失、无效或用户不存在于DB)。路径: %s, IP: %s", r.URL.Path, r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","message":"Authentication required"}`))
			return
		}
		if claims.Role != "admin" {
			log.Printf("RequireAdmin: 访问被拒绝 (用户 '%d' 角色 '%s' 非管理员)。路径: %s, IP: %s", claims.ID, claims.Role, r.URL.Path, r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden","message":"Admin access required"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
