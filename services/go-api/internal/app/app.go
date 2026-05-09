package app

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"demo-number-plates/services/go-api/internal/config"
	"demo-number-plates/services/go-api/internal/httpapi"
	"demo-number-plates/services/go-api/internal/service"
	"demo-number-plates/services/go-api/internal/storage"
)

type App struct {
	cfg         config.Config
	db          *storage.PostgresStore
	redis       *storage.RedisStore
	httpServer  *httpapi.Server
	ready       atomic.Bool
	initMessage atomic.Value
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	db, err := storage.NewPostgresStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	redis, err := storage.NewRedisStore(ctx, cfg)
	if err != nil {
		db.Close()
		return nil, err
	}

	app := &App{cfg: cfg, db: db, redis: redis}
	queryService := service.NewQueryService(db, redis)
	httpServer, err := httpapi.New(cfg, queryService, redis, app.IsReady, app.InitError)
	if err != nil {
		_ = redis.Close()
		db.Close()
		return nil, err
	}
	app.httpServer = httpServer
	return app, nil
}

func (a *App) Run() error {
	go a.initialize()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- a.httpServer.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownGracePeriod)
		defer cancel()
		if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("serve http: %w", err)
		}
	}

	a.close()
	return nil
}

func (a *App) initialize() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{name: "postgres ping", fn: func(ctx context.Context) error {
			return retry(ctx, 30, 2*time.Second, a.db.Ping)
		}},
		{name: "redis ping", fn: func(ctx context.Context) error {
			return retry(ctx, 30, 2*time.Second, a.redis.Ping)
		}},
		{name: "migrate schema", fn: a.db.Migrate},
		{name: "import data", fn: func(ctx context.Context) error {
			_, err := a.db.ImportIfEmpty(ctx)
			return err
		}},
		{name: "build redis indexes", fn: func(ctx context.Context) error {
			return a.redis.BuildIndexes(ctx, a.db)
		}},
	}

	for _, step := range steps {
		if err := step.fn(ctx); err != nil {
			message := fmt.Sprintf("%s failed: %v", step.name, err)
			log.Println(message)
			a.initMessage.Store(message)
			return
		}
		log.Printf("startup step complete: %s", step.name)
	}

	a.ready.Store(true)
	log.Println("application ready")
}

func retry(ctx context.Context, attempts int, delay time.Duration, fn func(context.Context) error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := fn(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		return fmt.Errorf("retry failed")
	}
	if strings.Contains(lastErr.Error(), "hostname resolving error") {
		return fmt.Errorf("dependency not reachable after retries: %w", lastErr)
	}
	return lastErr
}

func (a *App) IsReady() bool {
	return a.ready.Load()
}

func (a *App) InitError() string {
	value := a.initMessage.Load()
	if value == nil {
		return ""
	}
	message, _ := value.(string)
	return message
}

func (a *App) close() {
	if err := a.redis.Close(); err != nil {
		log.Printf("close redis: %v", err)
	}
	a.db.Close()
}
