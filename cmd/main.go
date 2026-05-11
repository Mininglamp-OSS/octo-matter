// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

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
	"github.com/Mininglamp-OSS/octo-matter/internal/dmworkim"
	"github.com/Mininglamp-OSS/octo-matter/internal/handler"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/middleware"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
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

	n, err := repository.RunMigrations(conn)
	if err != nil {
		log.Fatalf("auto-migration failed: %v", err)
	}
	if n > 0 {
		log.Printf("applied %d migration(s)", n)
	}

	// Repos
	matterRepo := repository.NewMatterRepo(sess)
	assigneeRepo := repository.NewAssigneeRepo(sess)
	participantRepo := repository.NewParticipantRepo(sess)
	channelRepo := repository.NewMatterChannelRepo(sess)
	timelineRepo := repository.NewTimelineRepo(sess)
	timelineAttachmentRepo := repository.NewTimelineAttachmentRepo(sess)
	activityRepo := repository.NewActivityRepo(sess)
	txMgr := repository.NewTxManager(sess)

	// Notifier
	notifier := notification.NewDmworkNotifier(cfg.DmworkIMURL, cfg.NotifyInternalToken)
	notifyWorker := notification.NewWorker(100, 4)
	defer notifyWorker.Shutdown()
	log.Printf("notification enabled via dmworkim %s/v1/internal/notify", cfg.DmworkIMURL)
	if cfg.NotifyInternalToken == "" {
		log.Printf("WARN: NOTIFY_INTERNAL_TOKEN not set — requests will be sent without X-Internal-Token")
	}

	// LLM
	llmClient := llm.New(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, time.Duration(cfg.LLMTimeout)*time.Second)
	log.Printf("llm gateway=%s model=%s timeout=%ds", cfg.LLMApiURL, cfg.LLMModel, cfg.LLMTimeout)
	if cfg.LLMApiKey == "" {
		log.Printf("WARN: LLM_API_KEY not set — extract/timeline will fail if the gateway requires auth")
	}

	// dmworkim API client (channel-membership lookups for the access checker)
	imClient := dmworkim.NewClient(cfg.DmworkIMURL, 5*time.Second)
	defer imClient.Close()
	log.Printf("dmworkim channel-membership client wired (base=%s)", cfg.DmworkIMURL)

	// Rate limiters for LLM-backed endpoints. Per design-v3.md §8 the
	// cooldown is 10s. Extract is keyed by uid (no matter exists yet) and
	// runs as gin middleware. Timeline is keyed by (matter_id, uid) inside
	// the service AFTER access check, so a forbidden caller cannot consume
	// the legitimate user's cooldown (PR #34 review r4259115029).
	uidKey := func(c *gin.Context) string { return c.GetString("uid") }
	extractLimiter := middleware.NewRateLimiter(10 * time.Second).Middleware(uidKey)
	timelineLimiter := middleware.NewRateLimiter(10 * time.Second)

	// Services
	matterSvc := service.NewMatterService(matterRepo, assigneeRepo, participantRepo, channelRepo, activityRepo, txMgr, imClient)
	timelineTx := timelineTxAdapter{mgr: txMgr}
	timelineSvc := service.NewTimelineService(llmClient, timelineRepo, timelineAttachmentRepo, matterRepo, matterSvc, matterSvc, timelineTx, participantRepo, assigneeRepo, timelineLimiter)
	extractSvc := service.NewExtractService(llmClient, matterSvc)
	activitySvc := service.NewActivityService(matterRepo, matterSvc, activityRepo)

	// Handlers
	matterH := handler.NewMatterHandler(matterSvc, notifier, notifyWorker)
	extractH := handler.NewExtractHandler(extractSvc)
	timelineH := handler.NewTimelineHandler(timelineSvc, matterSvc, notifier, notifyWorker)
	activityH := handler.NewActivityHandler(activitySvc)

	// Auth
	authMW := auth.AuthMiddleware(auth.Config{DmworkIMURL: cfg.DmworkIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.DmworkIMURL)

	// Readiness
	readiness := func() error { return conn.Ping() }

	// Router
	r := handler.SetupRouter(matterH, timelineH, activityH, extractH, extractLimiter, authMW, spaceMW, readiness)

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
