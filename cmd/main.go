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
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm/prompts"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm/promptstore"
	"github.com/Mininglamp-OSS/octo-matter/internal/middleware"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/octoim"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	log.Printf("starting matter-service env=%s octoim=%s", cfg.AppEnv, cfg.OctoIMURL)

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
	notifier := notification.NewOctoNotifier(cfg.OctoIMURL, cfg.NotifyInternalToken)
	notifyWorker := notification.NewWorker(100, 4)
	defer notifyWorker.Shutdown()
	log.Printf("notification enabled via octoim %s/v1/internal/notify", cfg.OctoIMURL)
	if cfg.NotifyInternalToken == "" {
		if cfg.AppEnv == config.AppEnvProd {
			log.Fatalf("FATAL: NOTIFY_INTERNAL_TOKEN is required in production")
		}
		log.Printf("WARN: NOTIFY_INTERNAL_TOKEN not set — notify requests sent without auth (dev only)")
	}

	// LLM
	llmClient := llm.New(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, time.Duration(cfg.LLMTimeout)*time.Second)
	log.Printf("llm gateway=%s model=%s timeout=%ds", cfg.LLMApiURL, cfg.LLMModel, cfg.LLMTimeout)
	if cfg.LLMApiKey == "" {
		log.Printf("WARN: LLM_API_KEY not set — extract/timeline will fail if the gateway requires auth")
	}

	// octoim API client (channel-membership lookups for the access checker)
	imClient := octoim.NewClient(cfg.OctoIMURL, 5*time.Second)
	defer imClient.Close()
	log.Printf("octoim channel-membership client wired (base=%s)", cfg.OctoIMURL)

	// Rate limiters for LLM-backed endpoints. Cooldown is 10s.
	// Extract is keyed by uid (no matter exists yet) and
	// runs as gin middleware. Timeline is keyed by (matter_id, uid) inside
	// the service AFTER access check, so a forbidden caller cannot consume
	// the legitimate user's cooldown.
	uidKey := func(c *gin.Context) string { return c.GetString("uid") }
	extractLimiter := middleware.NewRateLimiter(10 * time.Second).Middleware(uidKey)
	timelineLimiter := middleware.NewRateLimiter(10 * time.Second)

	// Prompt store: Langfuse (when configured) with embed fallback so a
	// Langfuse outage never blocks extraction. Tool schemas always come
	// from the embed FS — they are part of the engineering contract and
	// not editable via Langfuse.
	embedPrompts := promptstore.NewEmbed(prompts.FS)
	var extractStore, timelineStore promptstore.Store = embedPrompts, embedPrompts
	if cfg.LangfuseEnabled() {
		lf := promptstore.NewLangfuse(
			cfg.LangfuseHost,
			cfg.LangfusePublicKey,
			cfg.LangfuseSecretKey,
			promptstore.WithLangfuseLabel(cfg.LangfuseLabel),
			promptstore.WithLangfuseTimeout(cfg.LangfuseTimeout),
			promptstore.WithLangfuseCacheTTL(cfg.LangfuseCacheTTL),
			promptstore.WithLangfuseToolSource(embedPrompts),
		)
		chained := promptstore.NewFallback(lf, embedPrompts)
		extractStore, timelineStore = chained, chained
		log.Printf("prompt store: langfuse host=%s label=%s ttl=%s (embed fallback active)",
			cfg.LangfuseHost, cfg.LangfuseLabel, cfg.LangfuseCacheTTL)
	} else {
		log.Printf("prompt store: embed only (LANGFUSE_HOST/PUBLIC_KEY/SECRET_KEY not set)")
	}

	// Services
	matterSvc := service.NewMatterService(matterRepo, assigneeRepo, participantRepo, channelRepo, activityRepo, txMgr, imClient)
	timelineTx := timelineTxAdapter{mgr: txMgr}
	timelineSvc := service.NewTimelineService(llmClient, timelineRepo, timelineAttachmentRepo, matterRepo, matterSvc, matterSvc, timelineTx, participantRepo, assigneeRepo, timelineLimiter, service.WithTimelinePromptStore(timelineStore))
	extractSvc := service.NewExtractService(llmClient, matterSvc, service.WithExtractPromptStore(extractStore))
	activitySvc := service.NewActivityService(matterRepo, matterSvc, activityRepo)

	// Handlers
	matterH := handler.NewMatterHandler(matterSvc, notifier, notifyWorker)
	extractH := handler.NewExtractHandler(extractSvc)
	timelineH := handler.NewTimelineHandler(timelineSvc, matterSvc, notifier, notifyWorker)
	activityH := handler.NewActivityHandler(activitySvc)

	// Auth
	authMW := auth.AuthMiddleware(auth.Config{OctoIMURL: cfg.OctoIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.OctoIMURL)

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
