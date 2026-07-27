package config

import (
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

type ProcessRole string

const (
	RoleAll              ProcessRole = "all"
	RoleWeb              ProcessRole = "web"
	RoleWorker           ProcessRole = "worker"
	RoleRefreshScheduler ProcessRole = "refresh-scheduler"
	RoleDigestScheduler  ProcessRole = "digest-scheduler"
)

type SchedulerMode string

const (
	SchedulerLoop SchedulerMode = "loop"
	SchedulerOnce SchedulerMode = "once"
)

type RuntimeConfiguration struct {
	Role          ProcessRole
	SchedulerMode SchedulerMode
	Production    bool
	Version       string
}

type AuthConfiguration struct {
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type WebServerConfiguration struct {
	Env                    string
	Cors                   cors.Cors
	WebPort                string
	TLSCertFile            string
	TLSCertKeyFile         string
	ConnectionString       string
	PublicBaseURL          string
	AllowedOrigins         []string
	TrustProxyHeaders      bool
	MetricsToken           string
	AdminToken             string
	AuthRateLimitPerMinute int
}

type QueueConfiguration struct {
	Enabled        bool
	RedisURL       string
	Concurrency    int
	StrictPriority bool
}

type PushConfiguration struct {
	Enabled   bool
	KeyBase64 string
	KeyID     string
	TeamID    string
	Topic     string
}

type AppAttestConfiguration struct {
	Enabled      bool
	AppID        string
	AllowDev     bool
	Mode         string
	FeedbackMode string
}

type ObjectStorageConfiguration struct {
	Enabled         bool
	Partial         bool
	EndpointURL     string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Region          string
}

type VisionConfiguration struct {
	Enabled           bool
	Partial           bool
	CredentialsBase64 string
}

type GeminiConfiguration struct {
	Enabled bool
	APIKey  string
	Model   string
}

type Configuration struct {
	RuntimeConfig       *RuntimeConfiguration
	WebServerConfig     *WebServerConfiguration
	AuthConfig          *AuthConfiguration
	QueueConfig         *QueueConfiguration
	PushConfig          *PushConfiguration
	AppAttestConfig     *AppAttestConfiguration
	ObjectStorageConfig *ObjectStorageConfiguration
	VisionConfig        *VisionConfiguration
	GeminiConfig        *GeminiConfiguration
}

func loadRuntimeConfig() (*RuntimeConfiguration, error) {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	production := env == "production" || env == "prod"
	role := ProcessRole(strings.ToLower(strings.TrimSpace(os.Getenv("PROCESS_ROLE"))))
	if role == "" {
		role = RoleAll
	}
	switch role {
	case RoleAll, RoleWeb, RoleWorker, RoleRefreshScheduler, RoleDigestScheduler:
	default:
		return nil, fmt.Errorf("PROCESS_ROLE must be one of all|web|worker|refresh-scheduler|digest-scheduler, got %q", role)
	}
	if production && role == RoleAll {
		return nil, fmt.Errorf("PROCESS_ROLE must select a dedicated role in production")
	}

	schedulerMode := SchedulerMode(strings.ToLower(strings.TrimSpace(os.Getenv("SCHEDULER_MODE"))))
	if schedulerMode == "" {
		schedulerMode = SchedulerLoop
	}
	if schedulerMode != SchedulerLoop && schedulerMode != SchedulerOnce {
		return nil, fmt.Errorf("SCHEDULER_MODE must be loop or once, got %q", schedulerMode)
	}

	return &RuntimeConfiguration{
		Role:          role,
		SchedulerMode: schedulerMode,
		Production:    production,
		Version:       strings.TrimSpace(os.Getenv("DEPLOY_VERSION")),
	}, nil
}

func loadAuthConfig() (*AuthConfiguration, error) {
	secret := os.Getenv("AUTH_JWT_SECRET")
	if len(secret) < 32 {
		return nil, fmt.Errorf("AUTH_JWT_SECRET must be set to at least 32 bytes")
	}

	config := &AuthConfiguration{
		JWTSecret:       secret,
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 90 * 24 * time.Hour,
	}
	if ttl := os.Getenv("AUTH_ACCESS_TOKEN_TTL"); ttl != "" {
		parsed, err := time.ParseDuration(ttl)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("AUTH_ACCESS_TOKEN_TTL must be a positive duration")
		}
		config.AccessTokenTTL = parsed
	}
	if ttl := os.Getenv("AUTH_REFRESH_TOKEN_TTL"); ttl != "" {
		parsed, err := time.ParseDuration(ttl)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("AUTH_REFRESH_TOKEN_TTL must be a positive duration")
		}
		config.RefreshTokenTTL = parsed
	}
	return config, nil
}

func loadWebServerConfig(runtime *RuntimeConfiguration) (*WebServerConfiguration, error) {
	// A missing dotenv file is always fine: production providers inject env
	// directly, while local developers may still use .env.
	_ = godotenv.Load()

	config := &WebServerConfiguration{
		Env:                    os.Getenv("ENV"),
		TLSCertFile:            os.Getenv("TLS_CERT_FILE"),
		TLSCertKeyFile:         os.Getenv("TLS_CERT_KEY_FILE"),
		ConnectionString:       os.Getenv("DB_CONNECTION_STRING"),
		MetricsToken:           os.Getenv("METRICS_TOKEN"),
		AdminToken:             os.Getenv("ADMIN_TOKEN"),
		TrustProxyHeaders:      strings.EqualFold(os.Getenv("TRUST_PROXY_HEADERS"), "true"),
		AuthRateLimitPerMinute: 10,
	}
	if config.ConnectionString == "" {
		return nil, fmt.Errorf("must set DB_CONNECTION_STRING=<connection string>")
	}

	if webPort := strings.TrimSpace(os.Getenv("WEB_PORT")); webPort != "" {
		config.WebPort = normalizeListenAddress(webPort)
	} else if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		config.WebPort = normalizeListenAddress(port)
	} else {
		config.WebPort = ":8000"
	}

	publicOrigin, err := validatePublicBaseURL(os.Getenv("PUBLIC_BASE_URL"), runtime.Production)
	if err != nil {
		return nil, err
	}
	config.PublicBaseURL = publicOrigin
	if runtime.Production && len(config.AdminToken) < 32 {
		return nil, fmt.Errorf("ADMIN_TOKEN must be set to at least 32 bytes in production")
	}

	seen := map[string]struct{}{}
	appendOrigin := func(origin string) error {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return nil
		}
		if origin == "*" {
			return fmt.Errorf("ALLOWED_ORIGINS cannot contain wildcard origins")
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fmt.Errorf("invalid allowed origin %q", origin)
		}
		canonical := u.Scheme + "://" + u.Host
		if _, ok := seen[canonical]; !ok {
			seen[canonical] = struct{}{}
			config.AllowedOrigins = append(config.AllowedOrigins, canonical)
		}
		return nil
	}
	if err := appendOrigin(config.PublicBaseURL); err != nil {
		return nil, err
	}
	for _, origin := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if err := appendOrigin(origin); err != nil {
			return nil, err
		}
	}
	config.Cors = *cors.New(cors.Options{
		AllowedOrigins:   config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "If-None-Match", "If-Modified-Since", "X-Attest-Key-Id", "X-Attest-Assertion"},
		ExposedHeaders:   []string{"ETag", "Location", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           600,
	})

	if limit := os.Getenv("RATE_LIMIT_AUTH"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("RATE_LIMIT_AUTH must be a non-negative integer (0 disables)")
		}
		config.AuthRateLimitPerMinute = parsed
	}
	return config, nil
}

func normalizeListenAddress(value string) string {
	if strings.Contains(value, ":") {
		return value
	}
	return ":" + value
}

func validatePublicBaseURL(raw string, required bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return "", fmt.Errorf("PUBLIC_BASE_URL is required in production")
		}
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("PUBLIC_BASE_URL must be an absolute HTTPS origin")
	}
	if u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("PUBLIC_BASE_URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}

func loadQueueConfig() (*QueueConfiguration, error) {
	redisURL := strings.TrimSpace(os.Getenv("QUEUE_REDIS_URL"))
	if redisURL == "" {
		return nil, fmt.Errorf("QUEUE_REDIS_URL is required")
	}
	u, err := url.Parse(redisURL)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		return nil, fmt.Errorf("QUEUE_REDIS_URL must be a redis:// or rediss:// URL")
	}
	config := &QueueConfiguration{Enabled: true, RedisURL: redisURL, Concurrency: 10}
	if concurrency := os.Getenv("QUEUE_CONCURRENCY"); concurrency != "" {
		parsed, err := strconv.Atoi(concurrency)
		if err != nil || parsed < 1 {
			return nil, fmt.Errorf("QUEUE_CONCURRENCY must be a positive integer")
		}
		config.Concurrency = parsed
	}
	config.StrictPriority = strings.EqualFold(os.Getenv("QUEUE_STRICT_PRIORITY"), "true")
	return config, nil
}

func loadPushConfig() (*PushConfiguration, error) {
	config := &PushConfiguration{
		KeyBase64: os.Getenv("APNS_KEY_BASE64"),
		KeyID:     os.Getenv("APNS_KEY_ID"),
		TeamID:    os.Getenv("APNS_TEAM_ID"),
		Topic:     os.Getenv("APNS_TOPIC"),
	}
	set := countSet(config.KeyBase64, config.KeyID, config.TeamID, config.Topic)
	if set == 0 {
		return config, nil
	}
	if set != 4 {
		return nil, fmt.Errorf("APNS_KEY_BASE64, APNS_KEY_ID, APNS_TEAM_ID, and APNS_TOPIC must be set together")
	}
	config.Enabled = true
	return config, nil
}

func loadAppAttestConfig() (*AppAttestConfiguration, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ATTEST_MODE")))
	if mode == "" {
		mode = "log-only"
	}
	if mode != "off" && mode != "log-only" && mode != "required" {
		return nil, fmt.Errorf("APP_ATTEST_MODE must be one of off|log-only|required, got %q", mode)
	}
	teamID := strings.TrimSpace(os.Getenv("APP_ATTEST_TEAM_ID"))
	bundleID := strings.TrimSpace(os.Getenv("APP_ATTEST_BUNDLE_ID"))
	if bundleID == "" {
		bundleID = "au.com.shiftyjelly.podcasts"
	}
	if mode == "required" && teamID == "" {
		return nil, fmt.Errorf("APP_ATTEST_TEAM_ID is required when APP_ATTEST_MODE=required")
	}
	config := &AppAttestConfiguration{
		Enabled:      teamID != "" && mode != "off",
		AllowDev:     strings.EqualFold(os.Getenv("APP_ATTEST_ALLOW_DEV"), "true"),
		Mode:         mode,
		FeedbackMode: mode,
	}
	if teamID != "" {
		config.AppID = teamID + "." + bundleID
	}
	return config, nil
}

func loadFeatureConfig() (*ObjectStorageConfiguration, *VisionConfiguration, *GeminiConfiguration) {
	storage := &ObjectStorageConfiguration{
		EndpointURL:     strings.TrimSpace(os.Getenv("OBJECT_STORAGE_S3_URL")),
		Bucket:          strings.TrimSpace(os.Getenv("OBJECT_STORAGE_BUCKET")),
		AccessKeyID:     strings.TrimSpace(os.Getenv("OBJECT_STORAGE_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(os.Getenv("OBJECT_STORAGE_SECRET_ACCESS_KEY")),
		Region:          strings.TrimSpace(os.Getenv("OBJECT_STORAGE_REGION")),
	}
	storageSet := countSet(storage.EndpointURL, storage.Bucket, storage.AccessKeyID, storage.SecretAccessKey)
	storage.Enabled = storageSet == 4
	storage.Partial = storageSet > 0 && !storage.Enabled
	if storage.Region == "" {
		storage.Region = "auto"
	}

	visionCredentials := strings.TrimSpace(os.Getenv("GOOGLE_VISION_CREDENTIALS_BASE64"))
	vision := &VisionConfiguration{
		Enabled:           storage.Enabled && visionCredentials != "",
		Partial:           visionCredentials != "" && !storage.Enabled,
		CredentialsBase64: visionCredentials,
	}

	gemini := &GeminiConfiguration{
		APIKey: strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		Model:  strings.TrimSpace(os.Getenv("GEMINI_MODEL")),
	}
	gemini.Enabled = gemini.APIKey != ""
	if gemini.Model == "" {
		gemini.Model = "gemini-3.6-flash"
	}
	return storage, vision, gemini
}

func countSet(values ...string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func LoadConfigE() (*Configuration, error) {
	runtimeConfig, err := loadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	webServerConfig, err := loadWebServerConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}
	authConfig, err := loadAuthConfig()
	if err != nil {
		return nil, err
	}
	queueConfig, err := loadQueueConfig()
	if err != nil {
		return nil, err
	}
	pushConfig, err := loadPushConfig()
	if err != nil {
		return nil, err
	}
	appAttestConfig, err := loadAppAttestConfig()
	if err != nil {
		return nil, err
	}
	storage, vision, gemini := loadFeatureConfig()
	return &Configuration{
		RuntimeConfig:       runtimeConfig,
		WebServerConfig:     webServerConfig,
		AuthConfig:          authConfig,
		QueueConfig:         queueConfig,
		PushConfig:          pushConfig,
		AppAttestConfig:     appAttestConfig,
		ObjectStorageConfig: storage,
		VisionConfig:        vision,
		GeminiConfig:        gemini,
	}, nil
}

func LoadConfig() *Configuration {
	config, err := LoadConfigE()
	if err != nil {
		log.Fatal(err)
	}
	if config.ObjectStorageConfig.Partial {
		slog.Warn("Corpus disabled: object-storage configuration is incomplete")
	}
	if config.VisionConfig.Partial {
		slog.Warn("Avatar upload disabled: image scanning is configured but object storage is incomplete")
	}
	if !config.ObjectStorageConfig.Enabled && config.VisionConfig.CredentialsBase64 != "" {
		slog.Warn("Avatar upload disabled: complete object-storage credentials are required with Google Vision")
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		slog.Warn("OpenTelemetry export is not configured; traces and push metrics are disabled")
	}
	return config
}
