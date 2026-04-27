package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/auth"
	"github.com/Mininglamp-OSS/octo-matter/internal/config"
	"github.com/Mininglamp-OSS/octo-matter/internal/handler"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	log.Printf("starting todo-service env=%s auth=%s", cfg.AppEnv, cfg.AuthURL)

	conn, sess, err := repository.NewSession(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Repos
	goalRepo := repository.NewGoalRepo(sess)
	todoRepo := repository.NewTodoRepo(sess)
	assigneeRepo := repository.NewAssigneeRepo(sess)
	commentRepo := repository.NewCommentRepo(sess)
	attachmentRepo := repository.NewAttachmentRepo(sess)
	txMgr := repository.NewTxManager(sess)

	// Services
	goalSvc := service.NewGoalService(goalRepo, txMgr)
	todoSvc := service.NewTodoService(todoRepo, assigneeRepo, goalRepo, txMgr)
	commentSvc := service.NewCommentService(commentRepo, todoRepo, todoSvc)
	attachmentSvc := service.NewAttachmentService(attachmentRepo, todoRepo, todoSvc)

	// Handlers
	goalH := handler.NewGoalHandler(goalSvc)
	todoH := handler.NewTodoHandler(todoSvc)
	commentH := handler.NewCommentHandler(commentSvc)
	attachmentH := handler.NewAttachmentHandler(attachmentSvc)

	// Readiness probe: MySQL Ping via embedded *sql.DB.
	readiness := func() error { return conn.Ping() }

	// Router
	userAuth := auth.NewAuthMiddleware(cfg)
	r := handler.SetupRouter(goalH, todoH, commentH, attachmentH, userAuth, readiness)

	// Graceful shutdown: drain in-flight requests on SIGINT/SIGTERM.
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: r,
	}

	go func() {
		log.Printf("listening on :%s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down, draining requests (10s timeout)...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	conn.Close()
	log.Println("server exited cleanly")
}
