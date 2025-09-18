package main

import (
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
		// Docker not available - show helpful message for help command only
		if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help") {
			commands.ShowHelp()
			return
		}
		println("Error: Docker is not installed or not running")
		println("Please ensure Docker Desktop is installed and running")
		os.Exit(1)
	}

	// Create command handler
	handler := commands.NewHandler(dockerClient, cfg)

	// Execute command
	if err := handler.Execute(os.Args); err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}
