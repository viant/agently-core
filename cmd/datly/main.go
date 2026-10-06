package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/viant/agently-core/internal/datly/host"
	"github.com/viant/datly/cmd/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := command.Service{}
	os.Exit(service.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
