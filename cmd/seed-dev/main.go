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
	if cfg.AppEnv == "production" {
		fail(fmt.Errorf("development users cannot be created in production"))
	}
	request, err := identity.DevelopmentUsersRequestFromEnvironment()
	if err != nil {
		fail(err)
	}

	pool, err := database.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fail(err)
	}
	defer pool.Close()

	adminCreated, err := identity.BootstrapAdmin(context.Background(), pool.Pool, identity.BootstrapRequest{
		SchoolCode: request.SchoolCode, SchoolName: request.SchoolName,
		Username: request.AdminUsername, DisplayName: request.AdminDisplayName, Password: request.Password,
	})
	if err != nil {
		fail(err)
	}
	created, err := identity.BootstrapDevelopmentUsers(context.Background(), pool.Pool, request)
	if err != nil {
		fail(err)
	}
	fmt.Printf("development identities ready (admin_created=%t, users_created=%d)\n", adminCreated, created)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "development user seed failed:", err)
	os.Exit(1)
}
