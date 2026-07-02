package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

type Middleware struct {
	apiKey string
}

func NewMiddleware(apiKey string) Middleware {
	return Middleware{apiKey: apiKey}
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := requestToken(r)
		if !ok || !constantTimeEqual(token, m.apiKey) {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestToken(r *http.Request) (string, bool) {
	if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
		return token, true
	}
	for _, header := range []string{"X-API-Key", "X-Meitu-Proxy-API-Key"} {
		token := strings.TrimSpace(r.Header.Get(header))
		if token != "" {
			return token, true
		}
	}
	return "", false
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "unauthorized",
			"message": "缺少或无效的 Bearer 凭证",
		},
	})
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func constantTimeEqual(a string, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
