package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerAddr          string
	PostgresURL         string
	RedisAddr           string
	RedisPassword       string
	RedisDB             int
	DataDir             string
	ImportEnabled       bool
	ImportBatchSize     int
	APIKeys             map[string]struct{}
	RateLimitRequests   int
	RateLimitWindow     time.Duration
	BloomFalsePositive  float64
	BloomExpectedItems  uint64
	RedisSyncEnabled    bool
	RedisSyncBloom      bool
	RedisSyncBitmap     bool
	RedisSyncTrie       bool
	ReadHeaderTimeout   time.Duration
	ShutdownGracePeriod time.Duration
}

func Load() (Config, error) {
	postgresURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if postgresURL == "" {
		postgresAddr := envOrDefault("POSTGRES_ADDR", envOrDefault("POSTGRES_HOST", "localhost:5432"))
		postgresURL = fmt.Sprintf(
			"postgres://%s:%s@%s/%s?sslmode=disable",
			envOrDefault("POSTGRES_USER", "user"),
			envOrDefault("POSTGRES_PASSWORD", "password"),
			postgresAddr,
			envOrDefault("POSTGRES_DB", "number_plates"),
		)
	}

	apiKeys := parseAPIKeys(envOrDefault("API_KEYS", "demo-key"))
	if len(apiKeys) == 0 {
		return Config{}, fmt.Errorf("at least one API key is required")
	}

	rateLimitWindow, err := time.ParseDuration(envOrDefault("RATE_LIMIT_WINDOW", "1m"))
	if err != nil {
		return Config{}, fmt.Errorf("parse RATE_LIMIT_WINDOW: %w", err)
	}

	readHeaderTimeout, err := time.ParseDuration(envOrDefault("READ_HEADER_TIMEOUT", "5s"))
	if err != nil {
		return Config{}, fmt.Errorf("parse READ_HEADER_TIMEOUT: %w", err)
	}

	shutdownGrace, err := time.ParseDuration(envOrDefault("SHUTDOWN_GRACE_PERIOD", "15s"))
	if err != nil {
		return Config{}, fmt.Errorf("parse SHUTDOWN_GRACE_PERIOD: %w", err)
	}

	importBatchSize := envInt("IMPORT_BATCH_SIZE", 10000)
	if importBatchSize < 100 {
		importBatchSize = 100
	}

	bloomExpectedItems := uint64(envInt("BLOOM_EXPECTED_ITEMS", 1000000))
	if bloomExpectedItems == 0 {
		bloomExpectedItems = 1000000
	}

	return Config{
		ServerAddr:          envOrDefault("SERVER_ADDR", ":8081"),
		PostgresURL:         postgresURL,
		RedisAddr:           envOrDefault("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       strings.TrimSpace(os.Getenv("REDIS_PASSWORD")),
		RedisDB:             envInt("REDIS_DB", 0),
		DataDir:             envOrDefault("DATA_DIR", "/data"),
		ImportEnabled:       envBool("IMPORT_ENABLED", true),
		ImportBatchSize:     importBatchSize,
		APIKeys:             apiKeys,
		RateLimitRequests:   envInt("RATE_LIMIT_REQUESTS", 300),
		RateLimitWindow:     rateLimitWindow,
		BloomFalsePositive:  envFloat("BLOOM_FALSE_POSITIVE", 0.01),
		BloomExpectedItems:  bloomExpectedItems,
		RedisSyncEnabled:    envBool("REDIS_SYNC_ENABLED", true),
		RedisSyncBloom:      envBool("REDIS_SYNC_BLOOM", true),
		RedisSyncBitmap:     envBool("REDIS_SYNC_BITMAP", true),
		RedisSyncTrie:       envBool("REDIS_SYNC_TRIE", true),
		ReadHeaderTimeout:   readHeaderTimeout,
		ShutdownGracePeriod: shutdownGrace,
	}, nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseAPIKeys(value string) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		key := strings.TrimSpace(item)
		if key == "" {
			continue
		}
		keys[key] = struct{}{}
	}
	return keys
}
