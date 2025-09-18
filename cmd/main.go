package main

import (
	"fmt"
	"os"

	"github.com/aliasproject/devbox/internal/commands"
	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
)

func main() {
	// Initialize configuration
	cfg := config.Get()

	// Initialize Docker client
	dockerClient, err := docker.NewClient()
	if err != nil {
		// Docker not available - show helpful message
		if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help") {
			commands.ShowHelp()
			return
		}
		fmt.Fprintf(os.Stderr, "Error: Docker is not installed or not running\n")
		fmt.Fprintf(os.Stderr, "Please ensure Docker Desktop is installed and running\n")
		os.Exit(1)
	}

	// Create command handler
	handler := commands.NewHandler(dockerClient, cfg)

	// Execute command
	if err := handler.Execute(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
