package main

import (
	"context"
	"log"

	"wallet/internal/config"
	"wallet/internal/handler"
	"wallet/internal/repository/mysql"
	"wallet/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	repo, err := mysql.NewRepository(cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("connect to mysql: %v", err)
	}

	if err := repo.AutoMigrate(context.Background()); err != nil {
		log.Fatalf("apply schema: %v", err)
	}

	svc := service.NewWalletService(repo)
	h := handler.NewHandler(svc)
	router := handler.NewRouter(h)

	log.Printf("wallet service listening on :%s", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
