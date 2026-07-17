// Command podcast-backend serves a self-hosted Pocket Casts-compatible API.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/hbmartin/podcast-backend/artwork"
	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/handlers"
	"github.com/hbmartin/podcast-backend/itunes"
	"github.com/hbmartin/podcast-backend/middlewares"
	"github.com/hbmartin/podcast-backend/push"
	"github.com/hbmartin/podcast-backend/tasks"
	"github.com/hbmartin/podcast-backend/telemetry"
)

var configValues *config.Configuration

// tracingEnabled reports whether telemetry.Init activated OTel; the router
// is wrapped with otelhttp only then, keeping the no-tracing path free.
var tracingEnabled bool

// publicChain serves unauthenticated endpoints: trace, log, CORS.
func publicChain(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			configValues.WebServerConfig.Cors.Handler(
				http.HandlerFunc(handler))))
}

// authChain additionally requires a valid Bearer access token.
func authChain(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			configValues.WebServerConfig.Cors.Handler(
				auth.TokenAuthMiddleware(
					http.HandlerFunc(handler)))))
}

// optionalAuthChain attaches the user when a valid Bearer token is present
// but serves anonymous requests too (refresh works signed out; push
// registration piggybacked on it needs the identity).
func optionalAuthChain(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			configValues.WebServerConfig.Cors.Handler(
				auth.OptionalTokenMiddleware(
					http.HandlerFunc(handler)))))
}

func onlyLogMiddleware(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			http.HandlerFunc(handler)))
}

func setupRouter(db db.Store, queueClient *tasks.QueueClient, feedCrawler *crawler.Crawler, searcher itunes.Searcher, queuePing func(ctx context.Context) error) http.Handler {
	slog.Info("Starting API... \n")

	controllers := handlers.Handlers{
		Queries:       db,
		Queue:         queueClient,
		Config:        configValues.AuthConfig,
		Crawler:       feedCrawler,
		Search:        searcher,
		Images:        artwork.NewHTTPImageFetcher(),
		PublicBaseURL: configValues.WebServerConfig.PublicBaseURL,
		QueuePing:     queuePing,
	}

	// App Attest: verify device attestation on the fork-owned endpoints when
	// configured (docs/AppAttest.md). Unconfigured => endpoints behave as ModeOff.
	attestMode, attestFeedbackMode := attest.ModeOff, attest.ModeOff
	if configValues.AppAttestConfig != nil && configValues.AppAttestConfig.Enabled {
		verifier, err := attest.NewVerifier(configValues.AppAttestConfig.AppID, configValues.AppAttestConfig.AllowDev)
		if err != nil {
			log.Fatal(err)
		}
		controllers.AttestVerifier = verifier
		attestMode = attest.ParseMode(configValues.AppAttestConfig.Mode, attest.ModeLogOnly)
		attestFeedbackMode = attest.ParseMode(configValues.AppAttestConfig.FeedbackMode, attest.ModeLogOnly)
		slog.Info("App Attest enabled",
			"app_id", configValues.AppAttestConfig.AppID,
			"mode", attestMode.String(),
			"feedback_mode", attestFeedbackMode.String(),
			"allow_dev", configValues.AppAttestConfig.AllowDev)
	}

	router := http.NewServeMux()

	// limitedChain additionally rate limits by client IP; credential
	// endpoints only, to slow online brute-forcing.
	authLimiter := middlewares.NewRateLimiter(
		configValues.WebServerConfig.AuthRateLimitPerMinute,
		configValues.WebServerConfig.PublicBaseURL != "",
	)
	limitedChain := func(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
		return middlewares.TraceMiddleware(
			middlewares.LogMiddleware(
				configValues.WebServerConfig.Cors.Handler(
					authLimiter.Handler(
						http.HandlerFunc(handler)))))
	}

	// attestedOptionalChain: optional Bearer + App Attest assertion verification.
	attestedOptionalChain := func(mode attest.Mode, maxBody int64, endpoint string, handler http.HandlerFunc) http.Handler {
		return middlewares.TraceMiddleware(
			middlewares.LogMiddleware(
				configValues.WebServerConfig.Cors.Handler(
					auth.OptionalTokenMiddleware(
						controllers.AttestVerify(mode, maxBody, endpoint, handler)))))
	}
	// attestedAuthChain: required Bearer + App Attest assertion verification.
	attestedAuthChain := func(mode attest.Mode, maxBody int64, endpoint string, handler http.HandlerFunc) http.Handler {
		return middlewares.TraceMiddleware(
			middlewares.LogMiddleware(
				configValues.WebServerConfig.Cors.Handler(
					auth.TokenAuthMiddleware(
						controllers.AttestVerify(mode, maxBody, endpoint, handler)))))
	}
	// attestedPublicChain: unauthenticated + App Attest assertion verification.
	attestedPublicChain := func(mode attest.Mode, maxBody int64, endpoint string, handler http.HandlerFunc) http.Handler {
		return middlewares.TraceMiddleware(
			middlewares.LogMiddleware(
				configValues.WebServerConfig.Cors.Handler(
					controllers.AttestVerify(mode, maxBody, endpoint, handler))))
	}

	router.HandleFunc("OPTIONS /", configValues.WebServerConfig.Cors.HandlerFunc)
	router.Handle("GET /health", onlyLogMiddleware(controllers.GetHealth))
	router.Handle("GET /health.html", onlyLogMiddleware(controllers.GetHealthHTML))
	router.Handle("GET /metrics", promhttp.Handler())

	// api host role: account & auth (protobuf)
	router.Handle("POST /user/login", limitedChain(controllers.PostUserLogin))
	router.Handle("POST /user/register", limitedChain(controllers.PostUserRegister))
	router.Handle("POST /user/forgot_password", limitedChain(controllers.PostForgotPassword))
	router.Handle("POST /user/token", limitedChain(controllers.PostUserToken))
	router.Handle("POST /user/change_email", authChain(controllers.PostChangeEmail))
	router.Handle("POST /user/change_password", authChain(controllers.PostChangePassword))
	router.Handle("POST /user/delete_account", authChain(controllers.PostDeleteAccount))

	// api host role: sync & library (protobuf, authenticated)
	router.Handle("POST /user/sync/update", authChain(controllers.PostSyncUpdate))
	router.Handle("POST /user/last_sync_at", authChain(controllers.PostLastSyncAt))
	router.Handle("POST /user/podcast/list", authChain(controllers.PostUserPodcastList))
	router.Handle("POST /user/podcast/episodes", authChain(controllers.PostUserPodcastEpisodes))
	router.Handle("POST /user/playlist/list", authChain(controllers.PostUserPlaylistList))
	router.Handle("POST /user/bookmark/list", authChain(controllers.PostUserBookmarkList))
	router.Handle("POST /starred/list", authChain(controllers.PostStarredList))
	router.Handle("POST /up_next/sync", authChain(controllers.PostUpNextSync))
	router.Handle("POST /history/sync", authChain(controllers.PostHistorySync))
	router.Handle("POST /user/named_settings/update", authChain(controllers.PostNamedSettingsUpdate))
	router.Handle("POST /sync/update_episode", authChain(controllers.PostUpdateEpisode))
	router.Handle("POST /sync/update_episode_star", authChain(controllers.PostUpdateEpisodeStar))

	// refresh host role (JSON)
	router.Handle("POST /user/update", optionalAuthChain(controllers.PostRefreshUserUpdate))
	router.Handle("POST /podcasts/refresh", publicChain(controllers.PostPodcastsRefresh))
	router.Handle("POST /podcasts/show", publicChain(controllers.PostPodcastsShow))
	router.Handle("POST /podcasts/search", publicChain(controllers.PostPodcastsSearch))
	router.Handle("POST /import/opml", authChain(controllers.PostImportOpml))
	router.Handle("POST /import/export_feed_urls", authChain(controllers.PostExportFeedUrls))

	// cache host role (JSON)
	router.Handle("GET /mobile/podcast/full/{uuid}", publicChain(controllers.GetPodcastFull))
	router.Handle("GET /mobile/show_notes/full/{uuid}", publicChain(controllers.GetShowNotesFull))
	router.Handle("GET /mobile/episode/url/{podcastUuid}/{episodeUuid}", publicChain(controllers.GetEpisodeURL))
	router.Handle("GET /mobile/podcast/findbyepisode/{podcastUuid}/{episodeUuid}", publicChain(controllers.GetFindByEpisode))
	router.Handle("POST /mobile/podcast/episode/search", authChain(controllers.PostEpisodeSearchInPodcast))
	router.Handle("POST /episode/search", publicChain(controllers.PostEpisodeSearch))
	router.Handle("POST /search/combined", publicChain(controllers.PostCombinedSearch))

	// search host role
	router.Handle("GET /autocomplete/search", publicChain(controllers.GetAutocompleteSearch))

	// api host role: App Attest bootstrap (JSON) + crowdsourced transcripts
	// (gzipped protobuf), fork-owned (docs/AppAttest.md, docs/TranscriptContributions.md)
	router.Handle("GET /attest/challenge", limitedChain(controllers.GetAttestChallenge))
	router.Handle("POST /attest/enroll", limitedChain(controllers.PostAttestEnroll))
	router.Handle("POST /transcripts/contribute", attestedOptionalChain(attestMode, handlers.MaxContributeBody, "contribute", controllers.PostTranscriptContribute))
	router.Handle("POST /transcripts/sighting", attestedOptionalChain(attestMode, handlers.MaxSightingBody, "sighting", controllers.PostTranscriptSighting))

	// api host role: feedback (protobuf; authenticated and anonymous)
	router.Handle("POST /support/feedback", attestedAuthChain(attestFeedbackMode, handlers.MaxFeedbackBody, "feedback", controllers.PostSupportFeedback))
	router.Handle("POST /anonymous/feedback", attestedPublicChain(attestFeedbackMode, handlers.MaxFeedbackBody, "feedback", controllers.PostAnonymousFeedback))

	// api host role: ratings & stats (protobuf, authenticated)
	router.Handle("POST /user/podcast_rating/add", authChain(controllers.PostPodcastRatingAdd))
	router.Handle("POST /user/podcast_rating/show", authChain(controllers.PostPodcastRatingShow))
	router.Handle("GET /user/podcast_rating/list", authChain(controllers.GetPodcastRatingList))
	router.Handle("POST /user/stats/summary", authChain(controllers.PostStatsSummary))

	// cache host role: aggregate rating (public JSON)
	router.Handle("GET /podcast/rating/{uuid}", publicChain(controllers.GetPodcastRatingPublic))

	// api host role: social identity + moderation (protobuf; docs/Social.md).
	// Availability is the rate-limited typeahead; the public profile read is
	// optionally authenticated so the viewer's block relationship applies.
	router.Handle("POST /social/handle/availability", limitedChain(controllers.PostSocialHandleAvailability))
	router.Handle("POST /social/join", authChain(controllers.PostSocialJoin))
	router.Handle("POST /social/profile/get", authChain(controllers.PostSocialProfileGet))
	router.Handle("POST /social/profile/update", authChain(controllers.PostSocialProfileUpdate))
	router.Handle("POST /social/profile/public", optionalAuthChain(controllers.PostSocialProfilePublic))
	router.Handle("POST /social/block", authChain(controllers.PostSocialBlock))
	router.Handle("POST /social/unblock", authChain(controllers.PostSocialUnblock))
	router.Handle("POST /social/mute", authChain(controllers.PostSocialMute))
	router.Handle("POST /social/unmute", authChain(controllers.PostSocialUnmute))
	router.Handle("POST /social/report", authChain(controllers.PostSocialReport))
	router.Handle("POST /social/erase", authChain(controllers.PostSocialErase))
	// The web Profile Link page (ADR-0008 in the iOS repo): anonymous HTML view
	// of the same visibility-filtered public read.
	router.Handle("GET /u/{handle}", publicChain(controllers.GetPublicProfilePage))

	// api host role: written reviews + episode reactions (protobuf; Slice 3).
	router.Handle("POST /social/review/submit", authChain(controllers.PostReviewSubmit))
	router.Handle("POST /social/review/delete", authChain(controllers.PostReviewDelete))
	router.Handle("POST /podcast/reviews", optionalAuthChain(controllers.PostPodcastReviews))
	router.Handle("POST /social/reaction/set", authChain(controllers.PostReactionSet))
	router.Handle("POST /episode/reactions", optionalAuthChain(controllers.PostEpisodeReactions))

	// api host role: send-to-friend + shared-item inbox (protobuf; Slice 4).
	router.Handle("POST /social/share/send", authChain(controllers.PostShareSend))
	router.Handle("POST /social/inbox", authChain(controllers.PostInbox))
	router.Handle("POST /social/inbox/read", authChain(controllers.PostInboxRead))
	router.Handle("POST /social/inbox/delete", authChain(controllers.PostInboxDelete))

	// static host role: discover layout + catalog-backed sources (JSON)
	router.Handle("GET /discover/ios/content_v2.json", publicChain(controllers.GetDiscoverContent))
	router.Handle("GET /discover/ios/content_v3.json", publicChain(controllers.GetDiscoverContent))
	router.Handle("GET /discover/json/trending", publicChain(controllers.GetDiscoverTrending))
	router.Handle("GET /discover/json/popular", publicChain(controllers.GetDiscoverPopular))
	router.Handle("GET /discover/json/recent", publicChain(controllers.GetDiscoverRecent))
	router.Handle("GET /discover/json/categories", publicChain(controllers.GetDiscoverCategories))
	router.Handle("GET /discover/json/category/{name}", publicChain(controllers.GetDiscoverCategory))

	// sharing host role + share-link resolution (JSON)
	router.Handle("POST /share/list", publicChain(controllers.PostShareList))
	router.Handle("GET /l/{code}", publicChain(controllers.GetSharedList))
	router.Handle("POST /podcast/{uuid}", publicChain(controllers.PostSharePodcast))
	router.Handle("POST /episode/{uuid}", publicChain(controllers.PostShareEpisode))

	// static host role: artwork + color metadata
	router.Handle("GET /discover/images/metadata/{file}", publicChain(controllers.GetDiscoverImageMetadata))
	router.Handle("GET /discover/images/{size}/{file}", publicChain(controllers.GetDiscoverImage))

	return router
}

func initDB(ctx context.Context, configValues *config.Configuration) (db.Store, func()) {
	if err := db.Init(configValues.WebServerConfig.ConnectionString); err != nil {
		log.Fatal(err)
	}

	conn, err := pgxpool.New(ctx, configValues.WebServerConfig.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}

	store := db.NewStore(conn)

	return store, conn.Close
}

func startWebServer(querier db.Store, queueClient *tasks.QueueClient, feedCrawler *crawler.Crawler, searcher itunes.Searcher, queuePing func(ctx context.Context) error, configValues *config.Configuration, cancel context.CancelFunc) func(ctx context.Context) error {
	slog.Info("Setting up API router...\n")

	router := setupRouter(querier, queueClient, feedCrawler, searcher, queuePing)
	if tracingEnabled {
		router = otelhttp.NewHandler(router, "podcast-backend",
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
			}))
	}

	srv := &http.Server{
		Addr:              configValues.WebServerConfig.WebPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	useTls := configValues.WebServerConfig.TLSCertFile != "" && configValues.WebServerConfig.TLSCertKeyFile != ""

	slog.Info("Starting web server", "port", configValues.WebServerConfig.WebPort, "tls", useTls)

	go func() {
		var err error
		if useTls {
			err = srv.ListenAndServeTLS(configValues.WebServerConfig.TLSCertFile, configValues.WebServerConfig.TLSCertKeyFile)
		} else {
			err = srv.ListenAndServe()
		}

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Error starting server", "error", err)
			cancel()
		}
	}()

	return srv.Shutdown
}

// refreshScheduler periodically enqueues a sweep of catalog podcasts whose
// next_refresh_at has passed.
func refreshScheduler(ctx context.Context, queueClient *tasks.QueueClient) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := queueClient.EnqueueRefreshDuePodcasts(ctx); err != nil {
				slog.Warn("Unable to enqueue podcast refresh sweep", "error", err)
			}
		}
	}
}

// probeURL derives the local /health URL for the container probe. The listen
// address may be ":8000", "0.0.0.0:8000", "localhost:8000", or a bare port —
// the probe always connects to loopback with that port. TLS-serving instances
// are probed over https.
func probeURL(webPort string, useTLS bool) string {
	port := "8000"
	if webPort != "" {
		if idx := strings.LastIndex(webPort, ":"); idx >= 0 {
			port = webPort[idx+1:]
		} else {
			port = webPort
		}
	}

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	return scheme + "://127.0.0.1:" + port + "/health"
}

// runHealthProbe implements the container HEALTHCHECK: GET /health on the
// local server and exit 0/1. It must work inside the scratch image, where
// there is no shell or curl.
func runHealthProbe() int {
	useTLS := os.Getenv("TLS_CERT_FILE") != "" && os.Getenv("TLS_CERT_KEY_FILE") != ""

	client := &http.Client{Timeout: 5 * time.Second}
	if useTLS {
		// the loopback connection won't match the cert's hostnames, and a
		// self-signed cert is common; the probe only checks liveness
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	resp, err := client.Get(probeURL(os.Getenv("WEB_PORT"), useTLS))
	if err != nil {
		fmt.Fprintln(os.Stderr, "health probe failed:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "health probe: status", resp.StatusCode)
		return 1
	}
	return 0
}

func main() {
	healthProbe := flag.Bool("health", false, "probe the local server's /health endpoint and exit (container HEALTHCHECK)")
	flag.Parse()
	if *healthProbe {
		os.Exit(runHealthProbe())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("loading .env file...\n")
	configValues = config.LoadConfig()

	otelShutdown, otelEnabled, err := telemetry.Init(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if otelEnabled {
		slog.Info("OpenTelemetry tracing enabled")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Error("Error shutting down tracer provider", "error", err)
		}
	}()
	tracingEnabled = otelEnabled

	slog.Info("Init auth...\n")
	auth.Init(configValues.AuthConfig)

	slog.Info("Init DB...\n")
	querier, dbDispose := initDB(ctx, configValues)
	defer dbDispose()

	feedCrawler := &crawler.Crawler{DB: querier, Fetcher: crawler.NewHTTPFetcher()}
	searcher := itunes.NewClient(os.Getenv("ITUNES_BASE_URL"))

	// APNs push: new-episode alerts, delivered via the task queue when it is
	// enabled, in-process otherwise.
	var notifier *push.Notifier
	if configValues.PushConfig.Enabled {
		slog.Info("Init APNs push...")
		sender, err := push.NewClientFromFile(
			configValues.PushConfig.KeyFile,
			configValues.PushConfig.KeyID,
			configValues.PushConfig.TeamID,
			configValues.PushConfig.Topic,
			configValues.PushConfig.Endpoint,
		)
		if err != nil {
			log.Fatal(err)
		}
		notifier = &push.Notifier{DB: querier, Sender: sender}
	}

	// Initialize and start the background worker server and the queue
	// client used by API handlers to enqueue tasks.
	var worker *tasks.WorkerServer
	var queueClient *tasks.QueueClient
	if configValues.QueueConfig.Enabled {
		slog.Info("Starting background worker server...")
		worker = tasks.NewWorkerServer(configValues, querier, feedCrawler, notifier)
		go func() {
			if err := worker.Start(); err != nil {
				slog.Error("Asynq server failed to start", "error", err)
				stop()
			}
		}()

		queueClient = tasks.NewQueueClient(
			configValues.QueueConfig.RedisAddress,
			configValues.QueueConfig.RedisPassword,
			configValues.QueueConfig.RedisDb,
		)
		defer func() {
			if err := queueClient.Close(); err != nil {
				slog.Error("Error closing queue client", "error", err)
			}
		}()
	}

	if notifier != nil {
		if queueClient != nil {
			feedCrawler.OnNewEpisodes = func(podcastUuid string, episodeUuids []string) {
				if err := queueClient.EnqueueNotifyNewEpisodes(context.Background(), podcastUuid, episodeUuids); err != nil {
					slog.Warn("Unable to enqueue push delivery", "podcast", podcastUuid, "error", err)
				}
			}
		} else {
			directNotifier := notifier
			feedCrawler.OnNewEpisodes = func(podcastUuid string, episodeUuids []string) {
				go func() {
					sendCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					directNotifier.NotifyNewEpisodes(sendCtx, podcastUuid, episodeUuids)
				}()
			}
		}
	}

	if queueClient != nil {
		go refreshScheduler(ctx, queueClient)
	}

	// /health reports the queue's Redis as a dependency when enabled
	var queuePing func(ctx context.Context) error
	if configValues.QueueConfig.Enabled {
		queueRedis := redis.NewClient(&redis.Options{
			Addr:     configValues.QueueConfig.RedisAddress,
			Password: configValues.QueueConfig.RedisPassword,
			DB:       configValues.QueueConfig.RedisDb,
		})
		defer queueRedis.Close()
		queuePing = func(ctx context.Context) error {
			return queueRedis.Ping(ctx).Err()
		}
	}

	webDispose := startWebServer(querier, queueClient, feedCrawler, searcher, queuePing, configValues, stop)

	// Block until an interrupt signal is received
	<-ctx.Done()
	slog.Info("Shutting down servers gracefully...")

	// Gracefully close the web server, then the worker pool
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := webDispose(shutdownCtx); err != nil {
		slog.Error("Error shutting down web server", "error", err)
	}
	if worker != nil {
		worker.Shutdown()
	}
	slog.Info("All services stopped.")
}
