package main

import (
	"context"
	"log"

	"demo-number-plates/services/go-api/internal/app"
	"demo-number-plates/services/go-api/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(context.Background(), cfg)
	if err != nil {
		log.Fatalf("create application: %v", err)
	}

	if err := application.Run(); err != nil {
		log.Fatalf("run application: %v", err)
	}
}
