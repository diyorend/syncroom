package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port           string
	DatabaseURL    string
	RedisURL       string
	JWTSecret      string
	JWTExpiryHours int
	// RoomIdleTimeoutMinutes controls how long a room with zero connected
	// clients stays in memory before the cleanup sweep removes it.
	RoomIdleTimeoutMinutes int
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, using environment variables")
	}

	expiryHours, _ := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "24"))
	idleMinutes, _ := strconv.Atoi(getEnv("ROOM_IDLE_TIMEOUT_MINUTES", "10"))

	return &Config{
		Port:                   getEnv("PORT", "8080"),
		DatabaseURL:            mustGetEnv("DATABASE_URL"),
		RedisURL:               getEnv("REDIS_URL", "redis://localhost:6379"),
		JWTSecret:              mustGetEnv("JWT_SECRET"),
		JWTExpiryHours:         expiryHours,
		RoomIdleTimeoutMinutes: idleMinutes,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
