package auth

import (
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"strings"
)

type contextKey string

const (
	ContextUserIDKey contextKey = "userID"
)

func JWTMiddleware(publicKey ed25519.PublicKey) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
				return
			}

			_, err := ParseToken(parts[1], publicKey)
			if err != nil {
				slog.Error("Invalid token", "error", err) // ← adiciona esse log
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
