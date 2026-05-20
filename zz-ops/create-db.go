package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"likespotifybot/utils"
	"likespotifybot/utils/db"
)

func main() {
	utils.Env.InitOps()

	schemaPath := filepath.Join(".", "assets", "db.sql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		panic(fmt.Errorf("read schema file: %w", err))
	}

	database, err := db.DatabaseManager.Init(context.Background())
	if err != nil {
		panic(fmt.Errorf("open db: %w", err))
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := database.ExecContext(ctx, string(schema)); err != nil {
		panic(fmt.Errorf("apply schema: %w", err))
	}

	fmt.Println("schema applied successfully")
}
