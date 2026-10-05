package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	request, err := identity.BootstrapRequestFromEnvironment()
	if err != nil {
		fail(err)
	}

	pool, err := database.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fail(err)
	}
	defer pool.Close()

	created, err := identity.BootstrapAdmin(context.Background(), pool.Pool, request)
	if err != nil {
		fail(err)
	}
	if created {
		fmt.Println("system administrator created")
		return
	}
	fmt.Println("system administrator already exists; no changes made")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "administrator bootstrap failed:", err)
	os.Exit(1)
}
