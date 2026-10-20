package main

import (
	"fmt"
	"os"

	"github.com/aliasproject/ingot/internal/commands"
	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/docker"
	"github.com/aliasproject/ingot/internal/utils"
)

func main() {
	// Initialize configuration
	cfg := config.Get()

	// Initialize Docker client
	dockerClient, err := docker.NewClient()
	if err != nil {
		fmt.Println("Error initializing Docker client:", err)
		// Docker not available - show helpful message
		if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help") {
			commands.ShowHelp()
			return
		}
		// `doctor` is exactly the command someone reaches for when Docker
		// is the problem, so it must still produce a report without it.
		if len(os.Args) > 1 && os.Args[1] == "doctor" {
			commands.DoctorWithoutDocker(err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: Docker is not installed or not running\n")
		fmt.Fprintf(os.Stderr, "Please ensure Docker is installed and running\n")
		os.Exit(1)
	}

	// Create command handler
	handler := commands.NewHandler(dockerClient, cfg)

	// Check if this is an install command
	if len(os.Args) > 1 && os.Args[1] == "install" {
		if err := handler.Execute(os.Args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Check for first-time installation
	if !cfg.IsInstalled() {
		fmt.Println(utils.Colorize("[INGOT]", utils.ColorCyan) + " Welcome to Ingot! Installing for first-time use...")
		installer := commands.NewInstaller(dockerClient, cfg, handler.GetServices(), handler.GetSSL())
		if err := installer.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "Error during installation: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'ingot install' to retry - it resumes from wherever this left off.")
			os.Exit(1)
		}
		fmt.Println("\n" + utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Installation complete! You can now use Ingot.")
		fmt.Println("\nRun 'ingot help' to see available commands.")
		return
	}

	// Execute command
	if err := handler.Execute(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
