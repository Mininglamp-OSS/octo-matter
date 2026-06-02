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
	"github.com/Mininglamp-OSS/octo-matter/internal/middleware"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/octoim"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/runtime"
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
	timelineSvc := service.NewTimelineService(llmClient, timelineRepo, timelineAttachmentRepo, matterRepo, matterSvc, matterSvc, timelineTx, participantRepo, assigneeRepo, timelineLimiter, service.WithBotFeedActivityStore(activityRepo))
	extractSvc := service.NewExtractService(llmClient, matterSvc)
	activitySvc := service.NewActivityService(matterRepo, matterSvc, activityRepo)
	outputsSvc := service.NewOutputsService(matterRepo, matterSvc, timelineAttachmentRepo)

	// Handlers
	rtClient := runtime.NewClient(cfg.OctoServerURL, cfg.NotifyInternalToken, cfg.SelfBaseURL)
	botTaskRepo := repository.NewBotTaskRepo(sess)
	matterH := handler.NewMatterHandler(matterSvc, notifier, notifyWorker, rtClient, botTaskRepo)
	extractH := handler.NewExtractHandler(extractSvc)
	timelineH := handler.NewTimelineHandler(timelineSvc, matterSvc, notifier, notifyWorker, rtClient, botTaskRepo)
	activityH := handler.NewActivityHandler(activitySvc)
	internalH := handler.NewInternalHandler(timelineSvc, matterSvc, botTaskRepo)
	outputsH := handler.NewOutputsHandler(outputsSvc)

	// Auth
	authMW := auth.AuthMiddleware(auth.Config{OctoIMURL: cfg.OctoIMURL})
	spaceMW := auth.SpaceMiddleware(cfg.OctoIMURL)
	// PR-B: daemons authenticate to matter with daemon-scope JWTs issued by
	// octo-server. Verifier eagerly fetches the JWKS at boot (lazy retry on
	// first request if that fails). Env override matches fleet's convention.
	jwksURL := os.Getenv("OCTO_SERVER_JWKS_URL")
	if jwksURL == "" {
		jwksURL = "http://localhost:8090/.well-known/jwks.json"
	}
	daemonJWTVerifier := auth.NewJWTVerifier(jwksURL)
	daemonJWTMW := auth.DaemonJWTMiddleware(daemonJWTVerifier)

	// Readiness
	readiness := func() error { return conn.Ping() }

	// Router
	r := handler.SetupRouter(matterH, timelineH, activityH, outputsH, extractH, extractLimiter, internalH, authMW, spaceMW, daemonJWTMW, readiness)

	// PR-B sweeper: every 5 min, reclaim expired-lease bot_tasks back to
	// queued (attempt++) or dead-letter them once they hit max_attempts.
	// Plan §4 T2 + §8 require this — without it a daemon crash mid-task
	// leaves the row stuck in 'dispatched' forever.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	go runBotTaskSweeper(sweepCtx, botTaskRepo)

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
	sweepCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	conn.Close()
	log.Println("server exited cleanly")
}

// runBotTaskSweeper ticks every 5min and reclaims expired bot_task leases.
// Implements plan §4 T2 + §8: tasks stuck in 'dispatched' past their
// lease_until are reset to 'queued' (attempt++), and tasks that exceed
// max_attempts are dead-lettered to 'failed' with error_msg='exceeded
// max_attempts'. Without this loop, a daemon crash mid-task strands the
// row forever and no other daemon can pick it up.
func runBotTaskSweeper(ctx context.Context, repo *repository.BotTaskRepo) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	log.Println("bot_task sweeper started (5min tick)")
	// Fire once at boot so a fresh restart picks up any lease that
	// expired during downtime.
	sweepBotTaskOnce(ctx, repo)
	for {
		select {
		case <-ctx.Done():
			log.Println("bot_task sweeper stopped")
			return
		case <-ticker.C:
			sweepBotTaskOnce(ctx, repo)
		}
	}
}

func sweepBotTaskOnce(ctx context.Context, repo *repository.BotTaskRepo) {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reclaimed, deadLettered, err := repo.ReclaimExpired(tctx)
	if err != nil {
		log.Printf("[bot_task-sweeper] error: %v", err)
		return
	}
	if reclaimed > 0 || deadLettered > 0 {
		log.Printf("[bot_task-sweeper] reclaimed=%d dead_lettered=%d", reclaimed, deadLettered)
	}
}
