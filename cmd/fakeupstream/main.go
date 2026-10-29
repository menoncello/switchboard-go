// Command fakeupstream is a fake upstream for manual failover tests.
// It starts in "ok" mode; POST /__mode/quota and POST /__mode/ok switch it.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"sync/atomic"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9999", "listen address")
	flag.Parse()
	var quota atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /__mode/quota", func(w http.ResponseWriter, r *http.Request) { quota.Store(true) })
	mux.HandleFunc("POST /__mode/ok", func(w http.ResponseWriter, r *http.Request) { quota.Store(false) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if quota.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		var in struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &in)
		_ = json.NewEncoder(w).Encode(map[string]string{"model": in.Model})
	})
	log.Fatal(http.ListenAndServe(*addr, mux))
}
