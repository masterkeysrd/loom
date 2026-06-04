package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/masterkeysrd/loom/internal/studio"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: loom <command> [flags]")
		fmt.Println("Commands:")
		fmt.Println("  studio    Starts Loom Studio (OTLP receiver and dashboard API)")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "studio":
		runStudio()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runStudio() {
	flagSet := flag.NewFlagSet("studio", flag.ExitOnError)
	var (
		dbPath       string
		otlpGRPCPort int
		otlpHTTPPort int
		apiPort      int
	)

	flagSet.StringVar(&dbPath, "db", ".loom/telemetry.db", "Path to SQLite database")
	flagSet.IntVar(&otlpGRPCPort, "otlp-grpc-port", 4317, "Port for OTLP gRPC receiver")
	flagSet.IntVar(&otlpHTTPPort, "otlp-http-port", 4318, "Port for OTLP HTTP receiver")
	flagSet.IntVar(&apiPort, "api-port", 8080, "Port for Studio REST API")

	if err := flagSet.Parse(os.Args[2:]); err != nil {
		fmt.Printf("Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	s, err := studio.NewStudio(dbPath)
	if err != nil {
		fmt.Printf("Failed to initialize Studio: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Starting Loom Studio...\n")
	if err := s.Start(ctx, otlpGRPCPort, otlpHTTPPort, apiPort); err != nil {
		fmt.Printf("Studio stopped with error: %v\n", err)
		os.Exit(1)
	}
}
