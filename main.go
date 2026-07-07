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

	"github.com/jackc/pgx/v5/pgxpool"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"goapi-template/auth"
	"goapi-template/cache"
	"goapi-template/config"
	"goapi-template/db"
	"goapi-template/docs"
	"goapi-template/handlers"
	"goapi-template/middlewares"
	"goapi-template/tasks"
)

var configValues *config.Configuration

func withMiddlewares(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			configValues.WebServerConfig.Cors.Handler(
				auth.TokenAuthMiddleware(
					auth.OpaMiddleware(
						http.HandlerFunc(handler))))))
}

func onlyLogMiddleware(handler func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return middlewares.TraceMiddleware(
		middlewares.LogMiddleware(
			http.HandlerFunc(handler)))
}

func setupRouter(db db.Querier, queueClient *tasks.QueueClient) http.Handler {
	slog.Info("Starting API... \n")

	controllers := handlers.NewWithQueue(db, queueClient)
	router := http.NewServeMux()

	router.HandleFunc("OPTIONS /", configValues.WebServerConfig.Cors.HandlerFunc)
	router.Handle("GET /health", onlyLogMiddleware(controllers.GetHealth))

	router.Handle("GET /person/{id}", withMiddlewares(controllers.GetPerson))
	router.Handle("POST /person", withMiddlewares(controllers.PostPerson))
	router.Handle("PUT /person/{id}", withMiddlewares(controllers.PutPerson))
	router.Handle("DELETE /person/{id}", withMiddlewares(controllers.DeletePerson))

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

func initDB(ctx context.Context, configValues *config.Configuration) (db.Querier, func()) {
	if err := db.Init(configValues.WebServerConfig.ConnectionString); err != nil {
		log.Fatal(err)
	}

	conn, err := pgxpool.New(ctx, configValues.WebServerConfig.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}

	queries := db.New(conn)

	return queries, conn.Close
}

func initCache(querier db.Querier, configValues *config.CacheConfiguration) (db.Querier, func()) {
	// replace regular querier with caching querier if config says so
	if configValues.EnableTransparentCaching {
		cache := cache.NewRawCacher(configValues)

		return db.NewCachingQuerier(querier, cache), cache.Close
	}

	return querier, func() {}

}

func startWebServer(querier db.Querier, queueClient *tasks.QueueClient, configValues *config.Configuration) func(ctx context.Context) error {
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
		}
	}()

	return srv.Shutdown
}

// @securitydefinitions.oauth2.implicit					OAuth2Implicit
// @authorizationUrl										https://login.microsoftonline.com/9e6b9f31-c202-4cbd-a9b1-7e5cb3874384/oauth2/v2.0/authorize
// @tokenUrl												https://login.microsoftonline.com/9e6b9f31-c202-4cbd-a9b1-7e5cb3874384/oauth2/v2.0/token
// @scope.api://c571ab3c-0fde-43b2-b010-77e7bdd0d6f7/api	API
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

	slog.Info("Init Caching...")
	querier, cacheDispose := initCache(querier, configValues.CacheConfig)
	defer cacheDispose()

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
			}
		}()

		queueClient = tasks.NewQueueClient(
			configValues.QueueConfig.RedisAddress,
			configValues.QueueConfig.RedisPassword,
		)
		defer queueClient.Close()
	}

	webDispose := startWebServer(querier, queueClient, configValues)

	// Block until an interrupt signal is received
	<-ctx.Done()
	slog.Info("Shutting down servers gracefully...")

	// Gracefully close the web server, then the worker pool
	if err := webDispose(context.Background()); err != nil {
		slog.Error("Error shutting down web server", "error", err)
	}
	if worker != nil {
		worker.Shutdown()
	}
	slog.Info("All services stopped.")
}
