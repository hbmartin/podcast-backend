// Command podcast-backend serves a self-hosted Pocket Casts-compatible API.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hbmartin/podcast-backend/artwork"
	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/handlers"
	"github.com/hbmartin/podcast-backend/itunes"
	"github.com/hbmartin/podcast-backend/middlewares"
	"github.com/hbmartin/podcast-backend/tasks"
)

var configValues *config.Configuration

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

func onlyLogMiddleware(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			http.HandlerFunc(handler)))
}

func setupRouter(db db.Store, queueClient *tasks.QueueClient, feedCrawler *crawler.Crawler, searcher itunes.Searcher) http.Handler {
	slog.Info("Starting API... \n")

	controllers := handlers.Handlers{
		Queries: db,
		Queue:   queueClient,
		Config:  configValues.AuthConfig,
		Crawler: feedCrawler,
		Search:  searcher,
		Images:  artwork.NewHTTPImageFetcher(),
	}
	router := http.NewServeMux()

	router.HandleFunc("OPTIONS /", configValues.WebServerConfig.Cors.HandlerFunc)
	router.Handle("GET /health", onlyLogMiddleware(controllers.GetHealth))
	router.Handle("GET /health.html", onlyLogMiddleware(controllers.GetHealthHTML))

	// api host role: account & auth (protobuf)
	router.Handle("POST /user/login", publicChain(controllers.PostUserLogin))
	router.Handle("POST /user/register", publicChain(controllers.PostUserRegister))
	router.Handle("POST /user/forgot_password", publicChain(controllers.PostForgotPassword))
	router.Handle("POST /user/token", publicChain(controllers.PostUserToken))
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
	router.Handle("POST /user/update", publicChain(controllers.PostRefreshUserUpdate))
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

	// api host role: ratings & stats (protobuf, authenticated)
	router.Handle("POST /user/podcast_rating/add", authChain(controllers.PostPodcastRatingAdd))
	router.Handle("POST /user/podcast_rating/show", authChain(controllers.PostPodcastRatingShow))
	router.Handle("GET /user/podcast_rating/list", authChain(controllers.GetPodcastRatingList))
	router.Handle("POST /user/stats/summary", authChain(controllers.PostStatsSummary))

	// cache host role: aggregate rating (public JSON)
	router.Handle("GET /podcast/rating/{uuid}", publicChain(controllers.GetPodcastRatingPublic))

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

func startWebServer(querier db.Store, queueClient *tasks.QueueClient, feedCrawler *crawler.Crawler, searcher itunes.Searcher, configValues *config.Configuration, cancel context.CancelFunc) func(ctx context.Context) error {
	slog.Info("Setting up API router...\n")

	router := setupRouter(querier, queueClient, feedCrawler, searcher)

	srv := &http.Server{
		Addr: configValues.WebServerConfig.WebPort,
	}
	srv.Handler = router

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

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("loading .env file...\n")
	configValues = config.LoadConfig()

	slog.Info("Init auth...\n")
	auth.Init(configValues.AuthConfig)

	slog.Info("Init DB...\n")
	querier, dbDispose := initDB(ctx, configValues)
	defer dbDispose()

	feedCrawler := &crawler.Crawler{DB: querier, Fetcher: crawler.NewHTTPFetcher()}
	searcher := itunes.NewClient(os.Getenv("ITUNES_BASE_URL"))

	// Initialize and start the background worker server and the queue
	// client used by API handlers to enqueue tasks.
	var worker *tasks.WorkerServer
	var queueClient *tasks.QueueClient
	if configValues.QueueConfig.Enabled {
		slog.Info("Starting background worker server...")
		worker = tasks.NewWorkerServer(configValues, querier, feedCrawler)
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

	if queueClient != nil {
		go refreshScheduler(ctx, queueClient)
	}

	webDispose := startWebServer(querier, queueClient, feedCrawler, searcher, configValues, stop)

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
