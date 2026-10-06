// orderstub возвращает фиксированные ответы Order Service для локального запуска.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"loyaltyledger/internal/orders"
)

func main() {
	address := flag.String("addr", ":8081", "listen address")
	file := flag.String("fixtures", "testdata/orders.json", "fixture file")
	flag.Parse()
	data, err := os.ReadFile(*file)
	if err != nil {
		log.Fatal(err)
	}
	var fixtures []orders.Info
	if err = json.Unmarshal(data, &fixtures); err != nil {
		log.Fatal(err)
	}
	records := make(map[string]orders.Info, len(fixtures))
	for _, order := range fixtures {
		records[order.Number] = order
	}
	key := os.Getenv("ORDER_SERVICE_API_KEY")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /api/orders/{number}", func(w http.ResponseWriter, r *http.Request) {
		if key != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			w.WriteHeader(401)
			return
		}
		order, ok := records[r.PathValue("number")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(order)
	})
	server := &http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	log.Printf("Order Service stub listening on %s", *address)
	log.Fatal(server.ListenAndServe())
}
