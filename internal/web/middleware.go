package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

const maxPageBytes = 8 << 20

// A bounded response buffer lets recovery replace even a partially rendered
// page with a real 500 response, without leaking a stack trace to the client.
type responseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *responseBuffer) Header() http.Header { return b.header }
func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *responseBuffer) Write(data []byte) (int, error) {
	if b.body.Len()+len(data) > maxPageBytes {
		panic("response size limit exceeded")
	}
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(data)
}

func protect(next http.Handler, capacity int) http.Handler {
	slots := make(chan struct{}, capacity)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://groupietrackers.herokuapp.com; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "5")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Server is busy. Please retry shortly."})
			}
			return
		}
		defer func() {
			if problem := recover(); problem != nil {
				slog.Error("request recovered", "path", r.URL.Path, "reason", fmt.Sprint(problem))
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				if strings.HasPrefix(r.URL.Path, "/api/") {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
				}
				w.WriteHeader(http.StatusInternalServerError)
				if r.Method == http.MethodHead {
					return
				}
				if strings.HasPrefix(r.URL.Path, "/api/") {
					_, _ = w.Write([]byte("{\"error\":\"Internal server error. Please retry.\",\"status\":500}\n"))
				} else {
					_, _ = w.Write([]byte("500 — Internal server error. Please return to / and try again.\n"))
				}
			}
		}()
		buffer := &responseBuffer{header: make(http.Header)}
		next.ServeHTTP(buffer, r)
		for key, values := range buffer.header {
			w.Header()[key] = values
		}
		status := buffer.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(buffer.body.Bytes())
		}
	})
}
