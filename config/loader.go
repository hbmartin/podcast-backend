package config

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

type AuthConfiguration struct {
	Issuer          string   `json:"issuer"`
	JWKSUri         string   `json:"jwks_uri"`
	TokenSigningAlg []string `json:"id_token_signing_alg_values_supported"`
	Audience        string   `json:"audience"`
	ScopeClaim      string   `json:"scope_claim"`
	Scopes          []string `json:"scopes"`
	ClaimFields     []string `json:"claims"`
}

type WebServerConfiguration struct {
	Env              string
	Cors             cors.Cors
	EnableSwagger    bool
	WebPort          string
	TLSCertFile      string
	TLSCertKeyFile   string
	ConnectionString string
}

type CacheConfiguration struct {
	EnableTransparentCaching bool
	RedisAddress             string
	RedisPassword            string
	RedisDb                  int
	Expiration               time.Duration
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
	CacheConfig     *CacheConfiguration
	AuthConfig      *AuthConfiguration
	QueueConfig     *QueueConfiguration
}

func loadAuthConfig() (*AuthConfiguration, error) {
	configUrl, ok := os.LookupEnv("AUTH_CONFIG_URL")
	if !ok {
		return nil, fmt.Errorf("AUTH_CONFIG_URL is a required parameter")
	}

	config, err := readOpenIdConfigurationFromURL(configUrl)

	if err != nil {
		return nil, err
	}

	config.Audience = os.Getenv("AUTH_AUDIENCE")

	scopeClaim, ok := os.LookupEnv("AUTH_SCOPE_CLAIM")
	if !ok {
		scopeClaim = "scp"
	}
	config.ScopeClaim = scopeClaim

	if scopes, ok := os.LookupEnv("AUTH_SCOPES"); ok {
		config.Scopes = strings.Split(scopes, ",")
	}

	if authClaims, ok := os.LookupEnv("AUTH_CLAIMS"); ok {
		config.ClaimFields = strings.Split(authClaims, ",")
	}

	return config, nil
}

func readOpenIdConfigurationFromURL(configUrl string) (*AuthConfiguration, error) {
	if configUrl == "" {
		return nil, fmt.Errorf("cannot read OpenId configuration without URL")
	}

	response, err := http.Get(configUrl)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	target := &AuthConfiguration{}
	err = json.NewDecoder(response.Body).Decode(target)
	if err != nil {
		return nil, err
	}

	return target, nil
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

	if enableSwagger, ok := os.LookupEnv("ENABLE_SWAGGER"); ok {
		config.EnableSwagger = enableSwagger == "true"
	}

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

	return config, nil
}

func loadCacheConfig() (*CacheConfiguration, error) {
	config := &CacheConfiguration{}

	if enableTransparentCaching, ok := os.LookupEnv("ENABLE_TRANSPARENT_CACHE"); ok {
		config.EnableTransparentCaching = enableTransparentCaching == "true"
	}

	if !config.EnableTransparentCaching {
		// no reason to keep loading the cache config if we're not using it
		return config, nil
	}

	if redisAddress, ok := os.LookupEnv("REDIS_ADDRESS"); ok {
		config.RedisAddress = redisAddress
	} else {
		config.RedisAddress = "localhost:6379"
	}

	if redisDbStr, ok := os.LookupEnv("REDIS_DB"); ok {
		redisDb, _ := strconv.Atoi(redisDbStr)
		config.RedisDb = redisDb
	} else {
		config.RedisDb = 0
	}

	if expiration, ok := os.LookupEnv("REDIS_DEFAULT_EXPIRATION"); ok {
		expirationParsed, _ := time.ParseDuration(expiration)
		config.Expiration = expirationParsed
	} else {
		config.Expiration = time.Hour
	}

	config.RedisPassword, _ = os.LookupEnv("REDIS_PASSWORD")

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

	cacheConfig, err := loadCacheConfig()
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
		CacheConfig:     cacheConfig,
		QueueConfig:     queueConfig,
	}
}
