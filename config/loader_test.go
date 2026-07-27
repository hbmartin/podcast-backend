package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRuntime() *RuntimeConfiguration { return &RuntimeConfiguration{} }

func TestLoadWebConfig(t *testing.T) {
	t.Setenv("ENV", "test")
	t.Setenv("ALLOWED_ORIGINS", "https://web.example.com")
	t.Setenv("WEB_PORT", "localhost:8000")
	t.Setenv("TLS_CERT_FILE", "tls_cert_file")
	t.Setenv("TLS_CERT_KEY_FILE", "tls_cert_key_file")
	t.Setenv("DB_CONNECTION_STRING", "connection_string")

	config, err := loadWebServerConfig(testRuntime())
	require.NoError(t, err)
	assert.Equal(t, "localhost:8000", config.WebPort)
	assert.Equal(t, "connection_string", config.ConnectionString)
	assert.Equal(t, []string{"https://web.example.com"}, config.AllowedOrigins)
	assert.False(t, config.TrustProxyHeaders)
}

func TestLoadWebConfigPortPrecedence(t *testing.T) {
	t.Setenv("DB_CONNECTION_STRING", "connection_string")
	t.Setenv("WEB_PORT", "9000")
	t.Setenv("PORT", "7000")

	config, err := loadWebServerConfig(testRuntime())
	require.NoError(t, err)
	assert.Equal(t, ":9000", config.WebPort)

	t.Setenv("WEB_PORT", "")
	config, err = loadWebServerConfig(testRuntime())
	require.NoError(t, err)
	assert.Equal(t, ":7000", config.WebPort)

	t.Setenv("PORT", "")
	config, err = loadWebServerConfig(testRuntime())
	require.NoError(t, err)
	assert.Equal(t, ":8000", config.WebPort)
}

func TestLoadWebConfigMissingConnectionString(t *testing.T) {
	t.Setenv("DB_CONNECTION_STRING", "")
	_, err := loadWebServerConfig(testRuntime())
	assert.ErrorContains(t, err, "DB_CONNECTION_STRING")
}

func TestLoadPublicBaseURLPolicy(t *testing.T) {
	t.Setenv("DB_CONNECTION_STRING", "connection_string")
	t.Setenv("PUBLIC_BASE_URL", "https://pods.example.com")
	t.Setenv("ADMIN_TOKEN", "0123456789abcdef0123456789abcdef")   // gitleaks:allow -- deterministic test fixture
	t.Setenv("METRICS_TOKEN", "fedcba9876543210fedcba9876543210") // gitleaks:allow -- deterministic test fixture

	config, err := loadWebServerConfig(&RuntimeConfiguration{Production: true})
	require.NoError(t, err)
	assert.Equal(t, "https://pods.example.com", config.PublicBaseURL)
	assert.Contains(t, config.AllowedOrigins, "https://pods.example.com")

	for _, invalid := range []string{
		"http://pods.example.com",
		"https://user@pods.example.com",
		"https://pods.example.com/api",
		"https://pods.example.com?x=1",
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv("PUBLIC_BASE_URL", invalid)
			_, err := loadWebServerConfig(&RuntimeConfiguration{Production: true})
			assert.Error(t, err)
		})
	}
}

func TestProductionWebRequirements(t *testing.T) {
	t.Setenv("DB_CONNECTION_STRING", "connection_string")
	t.Setenv("PUBLIC_BASE_URL", "")
	_, err := loadWebServerConfig(&RuntimeConfiguration{Production: true})
	assert.ErrorContains(t, err, "PUBLIC_BASE_URL")

	t.Setenv("PUBLIC_BASE_URL", "https://pods.example.com")
	_, err = loadWebServerConfig(&RuntimeConfiguration{Production: true})
	assert.ErrorContains(t, err, "ADMIN_TOKEN")
}

func TestLoadAuthConfig(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef") // gitleaks:allow -- deterministic test fixture
	t.Setenv("AUTH_ACCESS_TOKEN_TTL", "1h")
	t.Setenv("AUTH_REFRESH_TOKEN_TTL", "720h")

	config, err := loadAuthConfig()
	require.NoError(t, err)
	assert.Equal(t, time.Hour, config.AccessTokenTTL)
	assert.Equal(t, 720*time.Hour, config.RefreshTokenTTL)
}

func TestLoadAuthConfigDefaults(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	config, err := loadAuthConfig()
	require.NoError(t, err)
	assert.Equal(t, time.Hour, config.AccessTokenTTL)
	assert.Equal(t, 90*24*time.Hour, config.RefreshTokenTTL)
}

func TestLoadAuthConfigRejectsMissingOrBadTTL(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "tooshort")
	_, err := loadAuthConfig()
	assert.Error(t, err)

	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL", "nonsense")
	_, err = loadAuthConfig()
	assert.Error(t, err)
}

func TestLoadQueueConfig(t *testing.T) {
	t.Setenv("QUEUE_REDIS_URL", "rediss://queue-password@queue-redis:6379/2")
	t.Setenv("QUEUE_CONCURRENCY", "25")
	t.Setenv("QUEUE_STRICT_PRIORITY", "true")

	config, err := loadQueueConfig()
	require.NoError(t, err)
	assert.True(t, config.Enabled)
	assert.Equal(t, "rediss://queue-password@queue-redis:6379/2", config.RedisURL)
	assert.Equal(t, 25, config.Concurrency)
	assert.True(t, config.StrictPriority)
}

func TestLoadQueueConfigRequiresURLAndValidScheme(t *testing.T) {
	t.Setenv("QUEUE_REDIS_URL", "")
	_, err := loadQueueConfig()
	assert.ErrorContains(t, err, "required")

	t.Setenv("QUEUE_REDIS_URL", "http://redis.example.com")
	_, err = loadQueueConfig()
	assert.ErrorContains(t, err, "redis:// or rediss://")
}

func TestRuntimeRoles(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("PROCESS_ROLE", "")
	_, err := loadRuntimeConfig()
	assert.ErrorContains(t, err, "dedicated role")

	t.Setenv("PROCESS_ROLE", "worker")
	t.Setenv("SCHEDULER_MODE", "once")
	config, err := loadRuntimeConfig()
	require.NoError(t, err)
	assert.Equal(t, RoleWorker, config.Role)
	assert.Equal(t, SchedulerOnce, config.SchedulerMode)
}

func TestFeatureActivationUsesCompleteCredentialSets(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_S3_URL", "https://account.r2.cloudflarestorage.com")
	t.Setenv("OBJECT_STORAGE_BUCKET", "podcasts")
	t.Setenv("OBJECT_STORAGE_ACCESS_KEY_ID", "key")
	storage, vision, gemini := loadFeatureConfig()
	assert.False(t, storage.Enabled)
	assert.True(t, storage.Partial)
	assert.False(t, vision.Enabled)
	assert.False(t, gemini.Enabled)

	t.Setenv("OBJECT_STORAGE_SECRET_ACCESS_KEY", "secret")
	t.Setenv("GOOGLE_VISION_CREDENTIALS_BASE64", "encoded")
	t.Setenv("GEMINI_API_KEY", "gemini")
	storage, vision, gemini = loadFeatureConfig()
	assert.True(t, storage.Enabled)
	assert.True(t, vision.Enabled)
	assert.True(t, gemini.Enabled)
	assert.Equal(t, "gemini-3.6-flash", gemini.Model)
}

func TestLoadAllConfig(t *testing.T) {
	t.Setenv("ENV", "test")
	t.Setenv("DB_CONNECTION_STRING", "connection_string")
	t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef") // gitleaks:allow -- deterministic test fixture
	t.Setenv("QUEUE_REDIS_URL", "redis://localhost:6379/1")

	config, err := LoadConfigE()
	require.NoError(t, err)
	assert.NotNil(t, config.RuntimeConfig)
	assert.NotNil(t, config.ObjectStorageConfig)
}
