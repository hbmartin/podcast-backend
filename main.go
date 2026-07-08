// gorest-template REST API
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

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"goapi-template/auth"
	"goapi-template/config"
	"goapi-template/db"
	"goapi-template/docs"
	"goapi-template/handlers"
	"goapi-template/middlewares"
	"goapi-template/tasks"
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

func setupRouter(db db.Store, queueClient *tasks.QueueClient) http.Handler {
	slog.Info("Starting API... \n")

	controllers := handlers.NewWithQueue(db, queueClient, configValues.AuthConfig)
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

	if configValues.WebServerConfig.EnableSwagger {
		slog.Info("Swagger enabled")
		swaggerHandler := httpSwagger.Handler(
			httpSwagger.URL("/swagger/doc.json"),
			httpSwagger.DeepLinking(true),
			httpSwagger.DocExpansion("none"),
			httpSwagger.DomID("swagger-ui"),
		)
		router.Handle("GET /swagger/", swaggerHandler)
	}

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

func startWebServer(querier db.Store, queueClient *tasks.QueueClient, configValues *config.Configuration, cancel context.CancelFunc) func(ctx context.Context) error {
	slog.Info("Setting up API router...\n")
	docs.SwaggerInfo.BasePath = "/"

	router := setupRouter(querier, queueClient)

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

	// Initialize and start the background worker server and the queue
	// client used by API handlers to enqueue tasks.
	var worker *tasks.WorkerServer
	var queueClient *tasks.QueueClient
	if configValues.QueueConfig.Enabled {
		slog.Info("Starting background worker server...")
		worker = tasks.NewWorkerServer(configValues, querier)
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

	webDispose := startWebServer(querier, queueClient, configValues, stop)

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
