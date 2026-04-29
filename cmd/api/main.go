package main

import (
	"log"
	"net/http"
	"os"

	"mafia-server/internal/app"
)

func main() {
	addr := getenv("HTTP_ADDR", ":8080")

	handler := app.NewHandler()
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	log.Printf("mafia server listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
