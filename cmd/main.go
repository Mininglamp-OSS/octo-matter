package main

import (
	"log"

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
	log.Printf("starting todo-service env=%s auth_mode=%s", cfg.AppEnv, cfg.AuthMode)

	sess, err := repository.NewSession(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Repos
	goalRepo := repository.NewGoalRepo(sess)
	todoRepo := repository.NewTodoRepo(sess)
	assigneeRepo := repository.NewAssigneeRepo(sess)
	commentRepo := repository.NewCommentRepo(sess)
	attachmentRepo := repository.NewAttachmentRepo(sess)

	// Services
	goalSvc := service.NewGoalService(goalRepo, todoRepo)
	todoSvc := service.NewTodoService(todoRepo, assigneeRepo)
	commentSvc := service.NewCommentService(commentRepo, todoRepo)
	attachmentSvc := service.NewAttachmentService(attachmentRepo, todoRepo)

	// Handlers
	goalH := handler.NewGoalHandler(goalSvc)
	todoH := handler.NewTodoHandler(todoSvc)
	commentH := handler.NewCommentHandler(commentSvc)
	attachmentH := handler.NewAttachmentHandler(attachmentSvc)

	// Router
	userAuth := auth.NewUserAuthMiddleware(cfg.AuthMode)
	r := handler.SetupRouter(goalH, todoH, commentH, attachmentH, userAuth)

	log.Printf("listening on :%s", cfg.ServerPort)
	if err := r.Run(":" + cfg.ServerPort); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
