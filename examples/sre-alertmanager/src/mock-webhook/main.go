package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := envOr("PORT", "9000")

	rec := newReceiver(os.Stdout)
	mux := http.NewServeMux()
	mux.Handle("/webhook", rec)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	fmt.Printf(`{"msg":"mock-webhook-receiver starting","port":%q}`+"\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, `{"msg":"server error","error":%q}`+"\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
