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
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	log.Printf("starting todo-service env=%s dmworkim=%s", cfg.AppEnv, cfg.DmworkIMURL)

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

	// Notifier — posts to dmworkim /v1/internal/notify (X-Internal-Token auth)
	notifier := notification.NewDmworkNotifier(cfg.DmworkIMURL, cfg.NotifyInternalToken)
	notifyWorker := notification.NewWorker(100, 4)
	defer notifyWorker.Shutdown()
	log.Printf("notification enabled via dmworkim %s/v1/internal/notify", cfg.DmworkIMURL)
	if cfg.NotifyInternalToken == "" {
		log.Printf("WARN: NOTIFY_INTERNAL_TOKEN not set — requests will be sent without X-Internal-Token")
	}

	// Services
	goalSvc := service.NewGoalService(goalRepo, txMgr)
	todoSvc := service.NewTodoService(todoRepo, assigneeRepo, goalRepo, txMgr)
	commentSvc := service.NewCommentService(commentRepo, todoRepo, todoSvc)
	attachmentSvc := service.NewAttachmentService(attachmentRepo, todoRepo, todoSvc)

	// Handlers
	goalH := handler.NewGoalHandler(goalSvc)
	todoH := handler.NewTodoHandler(todoSvc, notifier, notifyWorker)
	commentH := handler.NewCommentHandler(commentSvc, todoSvc, notifier, notifyWorker)
	attachmentH := handler.NewAttachmentHandler(attachmentSvc)

	// Auth: call dmworkim /v1/auth/verify + /v1/auth/verify-bot
	authMW := auth.AuthMiddleware(auth.Config{DmworkIMURL: cfg.DmworkIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.DmworkIMURL)

	// Readiness
	readiness := func() error { return conn.Ping() }

	// Router
	r := handler.SetupRouter(goalH, todoH, commentH, attachmentH, authMW, spaceMW, readiness)

	// Graceful shutdown
	srv := &http.Server{Addr: ":" + cfg.ServerPort, Handler: r}

	go func() {
		log.Printf("listening on :%s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	conn.Close()
	log.Println("server exited cleanly")
}
