package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/database"
)

func main() {
	var directory string
	flag.StringVar(&directory, "dir", "database", "migration directory")
	flag.Parse()

	cfg, err := config.Load()
	if err == nil {
		err = database.RunMigrations(context.Background(), cfg.DatabaseURL, directory)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migration failed:", err)
		os.Exit(1)
	}
	fmt.Println("database migrations are up to date")
}
