package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

type AuthConfiguration struct {
	// JWTSecret signs and verifies HS256 access tokens. Required, min 32 bytes.
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	// SharingCredential, when set, requires share/list requests to carry the
	// legacy sha1(datetime+credential) signature the client computes.
	SharingCredential string
}

type WebServerConfiguration struct {
	Env              string
	Cors             cors.Cors
	WebPort          string
	TLSCertFile      string
	TLSCertKeyFile   string
	ConnectionString string
	// PublicBaseURL, when set, is used verbatim as the base of generated
	// absolute links (share URLs, discover sources) instead of trusting
	// X-Forwarded-* request headers.
	PublicBaseURL string
	// AuthRateLimitPerMinute caps credential-endpoint requests per client IP
	// per minute (0 disables).
	AuthRateLimitPerMinute int
}

type QueueConfiguration struct {
	Enabled        bool
	RedisAddress   string
	RedisPassword  string
	RedisDb        int
	Concurrency    int
	StrictPriority bool
}

type Configuration struct {
	WebServerConfig *WebServerConfiguration
	AuthConfig      *AuthConfiguration
	QueueConfig     *QueueConfiguration
}

func loadAuthConfig() (*AuthConfiguration, error) {
	config := &AuthConfiguration{}

	secret, ok := os.LookupEnv("AUTH_JWT_SECRET")
	if !ok || len(secret) < 32 {
		return nil, fmt.Errorf("AUTH_JWT_SECRET must be set to at least 32 bytes")
	}
	config.JWTSecret = secret

	config.AccessTokenTTL = 24 * time.Hour
	if ttl, ok := os.LookupEnv("AUTH_ACCESS_TOKEN_TTL"); ok {
		parsed, err := time.ParseDuration(ttl)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("AUTH_ACCESS_TOKEN_TTL must be a positive duration")
		}
		config.AccessTokenTTL = parsed
	}

	config.RefreshTokenTTL = 365 * 24 * time.Hour
	if ttl, ok := os.LookupEnv("AUTH_REFRESH_TOKEN_TTL"); ok {
		parsed, err := time.ParseDuration(ttl)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("AUTH_REFRESH_TOKEN_TTL must be a positive duration")
		}
		config.RefreshTokenTTL = parsed
	}

	config.SharingCredential = os.Getenv("SHARING_CREDENTIAL")

	return config, nil
}

func loadWebServerConfig() (*WebServerConfiguration, error) {
	config := &WebServerConfiguration{}
	config.Env = os.Getenv("ENV")

	if err := godotenv.Load(); err != nil && config.Env == "" {
		return nil, err
	}

	allowedOrigin, _ := os.LookupEnv("ALLOWED_ORIGIN")

	config.Cors = *cors.New(cors.Options{
		AllowedOrigins: []string{allowedOrigin},
	})

	if webPort, ok := os.LookupEnv("WEB_PORT"); ok {
		config.WebPort = webPort
	} else {
		config.WebPort = "localhost:8000"
	}

	config.TLSCertFile, _ = os.LookupEnv("TLS_CERT_FILE")
	config.TLSCertKeyFile, _ = os.LookupEnv("TLS_CERT_KEY_FILE")

	if connectionString, ok := os.LookupEnv("DB_CONNECTION_STRING"); ok {
		config.ConnectionString = connectionString
	} else {
		return nil, fmt.Errorf("must set DB_CONNECTION_STRING=<connection string>")
	}

	config.PublicBaseURL = os.Getenv("PUBLIC_BASE_URL")

	config.AuthRateLimitPerMinute = 10
	if limit, ok := os.LookupEnv("RATE_LIMIT_AUTH"); ok {
		parsed, err := strconv.Atoi(limit)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("RATE_LIMIT_AUTH must be a non-negative integer (0 disables)")
		}
		config.AuthRateLimitPerMinute = parsed
	}

	return config, nil
}

func loadQueueConfig() (*QueueConfiguration, error) {
	config := &QueueConfiguration{}

	if enableTaskQueue, ok := os.LookupEnv("ENABLE_TASK_QUEUE"); ok {
		config.Enabled = enableTaskQueue == "true"
	}

	if !config.Enabled {
		// no reason to keep loading the queue config if we're not using it
		return config, nil
	}

	// the task queue defaults to the same Redis instance as the cache
	if redisAddress, ok := os.LookupEnv("QUEUE_REDIS_ADDRESS"); ok {
		config.RedisAddress = redisAddress
	} else if redisAddress, ok := os.LookupEnv("REDIS_ADDRESS"); ok {
		config.RedisAddress = redisAddress
	} else {
		config.RedisAddress = "localhost:6379"
	}

	if redisPassword, ok := os.LookupEnv("QUEUE_REDIS_PASSWORD"); ok {
		config.RedisPassword = redisPassword
	} else {
		config.RedisPassword, _ = os.LookupEnv("REDIS_PASSWORD")
	}

	if redisDbStr, ok := os.LookupEnv("QUEUE_REDIS_DB"); ok {
		redisDb, err := strconv.Atoi(redisDbStr)
		if err != nil || redisDb < 0 {
			return nil, fmt.Errorf("QUEUE_REDIS_DB must be a non-negative integer")
		}
		config.RedisDb = redisDb
	}

	if concurrencyStr, ok := os.LookupEnv("QUEUE_CONCURRENCY"); ok {
		concurrency, err := strconv.Atoi(concurrencyStr)
		if err != nil || concurrency < 1 {
			return nil, fmt.Errorf("QUEUE_CONCURRENCY must be a positive integer")
		}
		config.Concurrency = concurrency
	} else {
		config.Concurrency = 10
	}

	if strictPriority, ok := os.LookupEnv("QUEUE_STRICT_PRIORITY"); ok {
		config.StrictPriority = strictPriority == "true"
	}

	return config, nil
}

func LoadConfig() *Configuration {
	webServerConfig, err := loadWebServerConfig()
	if err != nil {
		log.Fatal(err)
	}

	authConfig, err := loadAuthConfig()
	if err != nil {
		log.Fatal(err)
	}

	queueConfig, err := loadQueueConfig()
	if err != nil {
		log.Fatal(err)
	}

	return &Configuration{
		WebServerConfig: webServerConfig,
		AuthConfig:      authConfig,
		QueueConfig:     queueConfig,
	}
}
