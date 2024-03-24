package main

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/xid"
)

// clientIDMiddleware .
type clientIDMiddleware struct {
	h http.Handler
}

type clientIDContextKey string

var ClientIDContextKey = clientIDContextKey("clientid")

func (b clientIDMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var id string
	cookies := r.Cookies()
	for _, c := range cookies {
		if c.Name == "battlr-cid" {
			_, err := xid.FromString(c.Value)
			if err == nil {
				id = c.Value
				break
			}
		}
	}
	if id == "" {
		id = xid.New().String()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "battlr-cid",
		Value:    id,
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})

	ctx := r.Context()
	ctx = context.WithValue(ctx, ClientIDContextKey, id)
	r = r.WithContext(ctx)
	b.h.ServeHTTP(w, r)
}

func ClientIDMiddleware() func(h http.Handler) clientIDMiddleware {
	fn := func(h http.Handler) clientIDMiddleware {
		return clientIDMiddleware{h: h}
	}
	return fn
}

func getClientID(ctx context.Context) string {
	var clientID string

	if v := ctx.Value(ClientIDContextKey); v != nil {
		if v, ok := v.(string); ok {
			clientID = v
		}
	}
	return "cookie:" + clientID
}
