// @title Mafia Game API
// @version 1.0
// @description Real-time Mafia game server API
// @host localhost:8080
// @BasePath /
// @schemes http https
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mafia-server/internal/app"
	"mafia-server/internal/persistence"
)

func main() {
	// PaaS platforms (Heroku, etc.) inject the listen port via $PORT.
	// Explicit HTTP_ADDR wins, then $PORT, then a local default.
	addr := getenv("HTTP_ADDR", "")
	if addr == "" {
		if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
	databaseURL := strings.TrimSpace(getenv("DATABASE_URL", ""))
	corsAllowedOrigins := splitCSV(getenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173"))
	wsAllowedOrigins := splitCSV(getenv("WS_ALLOWED_ORIGINS", "localhost:5173,127.0.0.1:5173"))
	loginRateLimitPerMinute := getenvInt("RATE_LIMIT_LOGIN_PER_MINUTE", 30)
	roomMutationRateLimitPerMinute := getenvInt("RATE_LIMIT_ROOM_MUTATION_PER_MINUTE", 60)
	liveKitURL := strings.TrimSpace(getenv("LIVEKIT_URL", ""))
	liveKitAPIKey := strings.TrimSpace(getenv("LIVEKIT_API_KEY", ""))
	liveKitAPISecret := strings.TrimSpace(getenv("LIVEKIT_API_SECRET", ""))

	store := persistence.NewNoopStore()
	if databaseURL != "" {
		postgresStore, err := connectPostgresStore(databaseURL, 30, time.Second)
		if err != nil {
			log.Fatalf("postgres init failed: %v", err)
		}
		defer postgresStore.Close()
		store = postgresStore
		log.Println("postgres persistence enabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler := app.NewHandlerWithConfig(ctx, store, app.SecurityConfig{
		CORSAllowedOrigins:         corsAllowedOrigins,
		WSAllowedOrigins:           wsAllowedOrigins,
		LoginRateLimitPerMinute:    loginRateLimitPerMinute,
		RoomMutationsRatePerMinute: roomMutationRateLimitPerMinute,
		LiveKitURL:                 liveKitURL,
		LiveKitAPIKey:              liveKitAPIKey,
		LiveKitAPISecret:           liveKitAPISecret,
	})
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		log.Printf("mafia server listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("%s=%q is invalid, fallback to %d", key, value, fallback)
		return fallback
	}

	return parsed
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		clean := strings.TrimSpace(part)
		if clean == "" {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func connectPostgresStore(databaseURL string, attempts int, delay time.Duration) (*persistence.PostgresStore, error) {
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		store, err := persistence.NewPostgresStore(ctx, databaseURL)
		cancel()
		if err == nil {
			return store, nil
		}

		lastErr = err
		log.Printf("postgres not ready (attempt %d/%d): %v", attempt, attempts, err)
		if attempt < attempts {
			time.Sleep(delay)
		}
	}

	if lastErr == nil {
		lastErr = errors.New("unknown postgres error")
	}
	return nil, lastErr
}
