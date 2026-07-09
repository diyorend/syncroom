package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/redis/go-redis/v9"

	"github.com/diyorend/syncroom/internal/config"
	"github.com/diyorend/syncroom/internal/handler"
	"github.com/diyorend/syncroom/internal/middleware"
	"github.com/diyorend/syncroom/internal/repository"
	"github.com/diyorend/syncroom/internal/room"
	"github.com/diyorend/syncroom/internal/service"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// --- Database ---
	ctx := context.Background()
	dbpool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer dbpool.Close()
	if err := dbpool.Ping(ctx); err != nil {
		slog.Error("database ping failed", "err", err)
		os.Exit(1)
	}
	slog.Info("database connected")

	// --- Redis ---
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		slog.Error("invalid redis URL", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("redis connected")

	// --- Repositories ---
	userRepo := repository.NewUserRepo(dbpool)
	roomRepo := repository.NewRoomRepo(dbpool)
	chatRepo := repository.NewChatRepo(dbpool)

	// --- Room manager (in-memory concurrent state) ---
	idleTimeout := time.Duration(cfg.RoomIdleTimeoutMinutes) * time.Minute
	manager := room.NewManager(rdb, idleTimeout)

	// --- Services ---
	authSvc := service.NewAuthService(userRepo, cfg.JWTSecret, cfg.JWTExpiryHours)
	roomSvc := service.NewRoomService(roomRepo)

	// --- Handlers ---
	authHandler := handler.NewAuthHandler(authSvc)
	roomHandler := handler.NewRoomHandler(roomSvc, chatRepo)
	wsHandler := handler.NewWSHandler(manager, roomSvc, chatRepo)

	// --- Echo router ---
	e := echo.New()
	e.HideBanner = true
	e.Use(echomiddleware.Logger())
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins: []string{"http://localhost:5173", "http://localhost:3000", "http://localhost"},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization},
		AllowMethods: []string{http.MethodGet, http.MethodPost},
	}))

	// Health check
	e.GET("/health", func(c echo.Context) error {
		if err := dbpool.Ping(c.Request().Context()); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "db_down"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Public auth routes
	e.POST("/api/auth/register", authHandler.Register)
	e.POST("/api/auth/login", authHandler.Login)

	// Room routes — creating a room requires auth; reading one is public
	e.POST("/api/rooms", roomHandler.Create, middleware.JWT(authSvc))
	e.GET("/api/rooms/:code", roomHandler.Get)

	// WebSocket — JWT middleware is applied with optional=true behaviour:
	// it runs extractToken and sets claims if a valid token is present, but
	// does NOT reject the request if no token is given (guests are welcome).
	// We achieve this by applying the middleware but guests simply get no
	// claims set — the handler's GetClaims() returns nil and it falls back
	// to the ?name= param.
	e.GET("/ws/:code", wsHandler.Connect, middleware.OptionalJWT(authSvc))

	// --- Background goroutines ---
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()

	go manager.StartIdleSweeper(bgCtx, 2*time.Minute)

	// --- Start server ---
	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
		}
	}()

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	bgCancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	if err := e.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}
