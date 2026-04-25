package main

import (
	"log"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
	"github.com/Mininglamp-OSS/octo-matter/internal/handler"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

func main() {
	cfg := config.Load()

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
	r := handler.SetupRouter(goalH, todoH, commentH, attachmentH)

	log.Printf("starting server on :%s", cfg.ServerPort)
	if err := r.Run(":" + cfg.ServerPort); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
