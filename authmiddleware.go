package main

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// bearerAuthMiddleware .
type bearerAuthMiddleware struct {
	h     http.Handler
	Token string
}

func (b bearerAuthMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.Token == "" {
		slog.Error("auth key not set")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ss := strings.SplitN(authHeader, " ", 2)
	if !(len(ss) == 2 && ss[0] == "Bearer") {
		w.WriteHeader(http.StatusUnauthorized)
		return

	}

	if subtle.ConstantTimeCompare([]byte(ss[1]), []byte(b.Token)) == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		return

	}
	b.h.ServeHTTP(w, r)
}

func BearerAuthMiddleware(token string) func(h http.Handler) http.Handler {
	fn := func(h http.Handler) http.Handler {
		return bearerAuthMiddleware{h: h, Token: token}
	}

	return fn
}
