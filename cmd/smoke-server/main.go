// Command smoke-server is the target the smoke test loads: a server that answers 200 to anything, so
// that the smoke run measures this repository's own client rather than a server's behaviour.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	address := flag.String("addr", "127.0.0.1:18080", "where to listen")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	log.Printf("smoke-server listening on %s", *address)
	log.Fatal(http.ListenAndServe(*address, nil))
}
