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
	log.Printf("starting matter-service env=%s dmworkim=%s", cfg.AppEnv, cfg.DmworkIMURL)

	conn, sess, err := repository.NewSession(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Repos
	matterRepo := repository.NewMatterRepo(sess)
	assigneeRepo := repository.NewAssigneeRepo(sess)
	participantRepo := repository.NewParticipantRepo(sess)
	channelRepo := repository.NewMatterChannelRepo(sess)
	commentRepo := repository.NewCommentRepo(sess)
	commentAttachmentRepo := repository.NewCommentAttachmentRepo(sess)
	txMgr := repository.NewTxManager(sess)

	// Notifier
	notifier := notification.NewDmworkNotifier(cfg.DmworkIMURL, cfg.NotifyInternalToken)
	notifyWorker := notification.NewWorker(100, 4)
	defer notifyWorker.Shutdown()
	log.Printf("notification enabled via dmworkim %s/v1/internal/notify", cfg.DmworkIMURL)
	if cfg.NotifyInternalToken == "" {
		log.Printf("WARN: NOTIFY_INTERNAL_TOKEN not set — requests will be sent without X-Internal-Token")
	}

	// Services
	matterSvc := service.NewMatterService(matterRepo, assigneeRepo, participantRepo, channelRepo, txMgr)
	commentTx := commentTxAdapter{mgr: txMgr}
	commentSvc := service.NewCommentService(commentRepo, commentAttachmentRepo, matterRepo, matterSvc, commentTx)

	// Handlers
	matterH := handler.NewMatterHandler(matterSvc, notifier, notifyWorker)
	commentH := handler.NewCommentHandler(commentSvc, matterSvc, notifier, notifyWorker)

	// Auth
	authMW := auth.AuthMiddleware(auth.Config{DmworkIMURL: cfg.DmworkIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.DmworkIMURL)

	// Readiness
	readiness := func() error { return conn.Ping() }

	// Router
	r := handler.SetupRouter(matterH, commentH, authMW, spaceMW, readiness)

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
