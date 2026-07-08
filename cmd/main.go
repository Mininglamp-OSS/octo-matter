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
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
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

	// i18n: build the message bundle and record the default fallback language.
	i18n.Init(cfg.DefaultLanguage)
	log.Printf("i18n enabled: default=%s supported=%v", i18n.DefaultLanguage(), i18n.SupportedLanguages())

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
	notifier := notification.NewOctoNotifier(cfg.OctoIMURL, cfg.NotifyInternalToken, cfg.DefaultLanguage)
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
	var llmClient service.LLMToolCaller
	switch cfg.LLMProvider {
	case "compat":
		llmClient = llm.New(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, time.Duration(cfg.LLMTimeout)*time.Second)
	case "openai":
		llmClient = llm.NewOpenAIOfficial(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, time.Duration(cfg.LLMTimeout)*time.Second)
	case "anthropic":
		llmClient = llm.NewAnthropicOfficial(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, time.Duration(cfg.LLMTimeout)*time.Second)
	default:
		log.Fatalf("invalid OCTO_LLM_PROVIDER %q (want compat, openai, or anthropic)", cfg.LLMProvider)
	}
	log.Printf("llm provider=%s gateway=%s model=%s timeout=%ds", cfg.LLMProvider, cfg.LLMApiURL, cfg.LLMModel, cfg.LLMTimeout)
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

	// Services
	matterSvc := service.NewMatterService(matterRepo, assigneeRepo, participantRepo, channelRepo, activityRepo, txMgr, imClient)
	timelineTx := timelineTxAdapter{mgr: txMgr}
	timelineSvc := service.NewTimelineService(llmClient, timelineRepo, timelineAttachmentRepo, matterRepo, matterSvc, matterSvc, timelineTx, participantRepo, assigneeRepo, timelineLimiter)
	extractSvc := service.NewExtractService(llmClient, matterSvc)
	activitySvc := service.NewActivityService(matterRepo, matterSvc, activityRepo)
	outputsSvc := service.NewOutputsService(matterRepo, matterSvc, timelineAttachmentRepo)

	// v2 services: transition guard, workflows, schedules, bot tasks, engine.
	transitionSvc := service.NewTransitionService(matterRepo, assigneeRepo, txMgr, cfg.PublicUIPath)
	// Smart summary needs a usable LLM; with no key the feature reports
	// LLM_NOT_CONFIGURED instead of failing mid-call.
	var v2LLM service.LLMToolCaller
	if cfg.LLMApiKey != "" {
		v2LLM = llmClient
	}
	v2Svc := service.NewV2Service(matterRepo, assigneeRepo, participantRepo, projectRepo, projectSourceRepo,
		feedbackRepo, outboxRepo, summaryRepo, activityRepo, agentCardRepo, txMgr, transitionSvc, matterSvc, v2LLM)
	v2Svc.SetDoorbell(notifier)
	botTaskSvc := service.NewBotTaskService(botTaskRepo, matterRepo, timelineRepo, activityRepo, transitionSvc)
	scheduleSvc := service.NewScheduleService(scheduleRepo, matterRepo, matterSvc, v2Svc, transitionSvc, cfg.ScheduleTick)

	// Engine loops: transactional-outbox dispatcher + two-tier watchdog.
	engineCtx, engineStop := context.WithCancel(context.Background())
	defer engineStop()
	engine := service.NewEngine(outboxRepo, matterRepo, transitionSvc, notifier, service.EngineConfig{
		DispatchInterval: cfg.OutboxDispatchInterval,
		RedeliverAfter:   cfg.OutboxRedeliverAfter,
		MaxRetries:       cfg.OutboxMaxRetries,
		WatchdogInterval: cfg.WatchdogInterval,
		WatchdogEnabled:  cfg.WatchdogEnabled,
		ReviveSilence:    cfg.WatchdogReviveSilence,
		LeafSLA:          cfg.WatchdogLeafSLA,
		BlockAfterRevive: cfg.WatchdogBlockAfterRevive,
	})
	engine.Start(engineCtx)
	scheduleSvc.Start(engineCtx)
	log.Printf("matter v2 engine started (outbox dispatch=%s watchdog=%s enabled=%t)", cfg.OutboxDispatchInterval, cfg.WatchdogInterval, cfg.WatchdogEnabled)

	// Handlers
	matterH := handler.NewMatterHandler(matterSvc, notifier, notifyWorker)
	extractH := handler.NewExtractHandler(extractSvc)
	timelineH := handler.NewTimelineHandler(timelineSvc, matterSvc, notifier, notifyWorker)
	activityH := handler.NewActivityHandler(activitySvc)
	outputsH := handler.NewOutputsHandler(outputsSvc)

	// Auth
	authMW := auth.AuthMiddleware(auth.Config{OctoIMURL: cfg.OctoIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.OctoIMURL)

	// Readiness
	readiness := func() error { return conn.Ping() }

	// Router
	r := handler.SetupRouter(matterH, timelineH, activityH, outputsH, extractH, extractLimiter, authMW, spaceMW, readiness)

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
