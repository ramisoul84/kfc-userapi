package main

import (
	"log"

	"github.com/ramisoul84/kfc-userapi/internal/config"
)

func main() {
	// Load configuration.
	_, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
