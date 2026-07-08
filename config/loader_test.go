package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadWebConfig(t *testing.T) {
	t.Setenv("ENV", "TEST")
	t.Setenv("ALLOWED_ORIGIN", "localhost:8000")
	t.Setenv("WEB_PORT", "localhost:8000")
	t.Setenv("TLS_CERT_FILE", "tls_cert_file")
	t.Setenv("TLS_CERT_KEY_FILE", "tls_cert_key_file")
	t.Setenv("DB_CONNECTION_STRING", "connection_string")

	config, err := loadWebServerConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost:8000", config.WebPort)
	assert.Equal(t, "connection_string", config.ConnectionString)
	assert.Contains(t, "TEST", config.Env)
	assert.Contains(t, "tls_cert_file", config.TLSCertFile)
	assert.Contains(t, "tls_cert_key_file", config.TLSCertKeyFile)
}

func TestLoadWebConfigDefaults(t *testing.T) {
	t.Setenv("ENV", "TEST")
	t.Setenv("DB_CONNECTION_STRING", "connection_string")

	config, err := loadWebServerConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost:8000", config.WebPort)
	assert.Equal(t, "connection_string", config.ConnectionString)
	assert.Contains(t, "TEST", config.Env)
	assert.Empty(t, config.TLSCertFile)
	assert.Empty(t, config.TLSCertKeyFile)
}

func TestLoadWebConfigMissingConnectionString(t *testing.T) {
	t.Setenv("ENV", "TEST")

	_, err := loadWebServerConfig()

	assert.Error(t, err, "must set DB_CONNECTION_STRING=<connection string>")
}

func TestLoadAuthConfig(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL", "1h")
	t.Setenv("AUTH_REFRESH_TOKEN_TTL", "720h")

	config, err := loadAuthConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "0123456789abcdef0123456789abcdef", config.JWTSecret)
	assert.Equal(t, time.Hour, config.AccessTokenTTL)
	assert.Equal(t, 720*time.Hour, config.RefreshTokenTTL)
}

func TestLoadAuthConfigDefaults(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")

	config, err := loadAuthConfig()

	assert.Nil(t, err)
	assert.Equal(t, 24*time.Hour, config.AccessTokenTTL)
	assert.Equal(t, 365*24*time.Hour, config.RefreshTokenTTL)
}

func TestLoadAuthConfigMissingSecret(t *testing.T) {
	_, err := loadAuthConfig()

	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "AUTH_JWT_SECRET must be set to at least 32 bytes")
}

func TestLoadAuthConfigShortSecret(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "tooshort")

	_, err := loadAuthConfig()

	assert.NotNil(t, err)
}

func TestLoadAuthConfigBadTTL(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL", "nonsense")

	_, err := loadAuthConfig()

	assert.NotNil(t, err)
}

func TestLoadCacheConfigDisableCache(t *testing.T) {
	t.Setenv("ENABLE_TRANSPARENT_CACHE", "false")

	config, _ := loadCacheConfig()

	assert.NotNil(t, config)
	assert.False(t, config.EnableTransparentCaching)
}

func TestLoadCacheConfigValidDefaults(t *testing.T) {
	t.Setenv("ENABLE_TRANSPARENT_CACHE", "true")

	config, _ := loadCacheConfig()

	assert.NotNil(t, config)
	assert.True(t, config.EnableTransparentCaching)
	assert.Equal(t, "localhost:6379", config.RedisAddress)
	assert.Equal(t, "", config.RedisPassword)
	assert.Equal(t, time.Hour, config.Expiration)
	assert.Equal(t, 0, config.RedisDb)
}

func TestLoadCacheConfigValid(t *testing.T) {
	t.Setenv("ENABLE_TRANSPARENT_CACHE", "true")
	t.Setenv("REDIS_ADDRESS", "localhost:6379")
	t.Setenv("REDIS_PASSWORD", "password")
	t.Setenv("REDIS_DB", "1")
	t.Setenv("REDIS_DEFAULT_EXPIRATION", "2h")

	config, _ := loadCacheConfig()

	assert.NotNil(t, config)
	assert.True(t, config.EnableTransparentCaching)
	assert.Equal(t, "localhost:6379", config.RedisAddress)
	assert.Equal(t, "password", config.RedisPassword)
	assert.Equal(t, time.Hour*2, config.Expiration)
	assert.Equal(t, 1, config.RedisDb)
}

func TestLoadAllConfig(t *testing.T) {
	t.Setenv("ENV", "TEST")
	t.Setenv("ALLOWED_ORIGIN", "localhost:8000")
	t.Setenv("WEB_PORT", "localhost:8000")
	t.Setenv("TLS_CERT_FILE", "tls_cert_file")
	t.Setenv("TLS_CERT_KEY_FILE", "tls_cert_key_file")
	t.Setenv("DB_CONNECTION_STRING", "connection_string")

	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENABLE_TRANSPARENT_CACHE", "false")

	config := LoadConfig()

	assert.NotNil(t, config)
}

func TestLoadQueueConfigDisabledByDefault(t *testing.T) {
	config, err := loadQueueConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.False(t, config.Enabled)
}

func TestLoadQueueConfigDefaults(t *testing.T) {
	t.Setenv("ENABLE_TASK_QUEUE", "true")

	config, err := loadQueueConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.True(t, config.Enabled)
	assert.Equal(t, "localhost:6379", config.RedisAddress)
	assert.Equal(t, 0, config.RedisDb)
	assert.Equal(t, 10, config.Concurrency)
	assert.False(t, config.StrictPriority)
}

func TestLoadQueueConfig(t *testing.T) {
	t.Setenv("ENABLE_TASK_QUEUE", "true")
	t.Setenv("QUEUE_REDIS_ADDRESS", "queue-redis:6379")
	t.Setenv("QUEUE_REDIS_PASSWORD", "queue-password")
	t.Setenv("QUEUE_REDIS_DB", "2")
	t.Setenv("QUEUE_CONCURRENCY", "25")
	t.Setenv("QUEUE_STRICT_PRIORITY", "true")

	config, err := loadQueueConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "queue-redis:6379", config.RedisAddress)
	assert.Equal(t, "queue-password", config.RedisPassword)
	assert.Equal(t, 2, config.RedisDb)
	assert.Equal(t, 25, config.Concurrency)
	assert.True(t, config.StrictPriority)
}

func TestLoadQueueConfigFallsBackToCacheRedis(t *testing.T) {
	t.Setenv("ENABLE_TASK_QUEUE", "true")
	t.Setenv("REDIS_ADDRESS", "cache-redis:6379")
	t.Setenv("REDIS_PASSWORD", "cache-password")

	config, err := loadQueueConfig()

	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "cache-redis:6379", config.RedisAddress)
	assert.Equal(t, "cache-password", config.RedisPassword)
	assert.Equal(t, 0, config.RedisDb)
}

func TestLoadQueueConfigInvalidConcurrency(t *testing.T) {
	t.Setenv("ENABLE_TASK_QUEUE", "true")
	t.Setenv("QUEUE_CONCURRENCY", "not-a-number")

	config, err := loadQueueConfig()

	assert.Nil(t, config)
	assert.NotNil(t, err)
}

func TestLoadQueueConfigInvalidRedisDb(t *testing.T) {
	t.Setenv("ENABLE_TASK_QUEUE", "true")
	t.Setenv("QUEUE_REDIS_DB", "-1")

	config, err := loadQueueConfig()

	assert.Nil(t, config)
	assert.NotNil(t, err)
}
