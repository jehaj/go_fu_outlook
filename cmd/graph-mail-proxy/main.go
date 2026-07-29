package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"graph-mail-proxy/internal/auth"
	"graph-mail-proxy/internal/config"
	"graph-mail-proxy/internal/graph"
	"graph-mail-proxy/internal/imap"
	"graph-mail-proxy/internal/smtp"
	"graph-mail-proxy/internal/store"
)

func main() {
	configPathFlag := flag.String("config", "~/.config/graph-mail-proxy/config.yaml", "Path to YAML configuration file")
	authOnlyFlag := flag.Bool("auth-only", false, "Run interactive device code authentication and exit")
	flag.Parse()

	expandedConfigPath := config.ExpandPath(*configPathFlag)
	cfg, err := config.LoadConfig(expandedConfigPath)
	if err != nil {
		log.Printf("Notice: Could not load config from %s (%v). Using default configuration.", expandedConfigPath, err)
		cfg = config.DefaultConfig()
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	log.Printf("Starting Local Graph-to-IMAP/SMTP Proxy...")
	log.Printf("SECURITY NOTICE: Bind addresses IMAP=%s, SMTP=%s (plaintext-over-loopback by design)", cfg.IMAP.BindAddr, cfg.SMTP.BindAddr)

	authMgr, err := auth.NewAuthManager(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize AuthManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *authOnlyFlag {
		log.Println("Starting interactive device-code authentication flow...")
		_, err := authMgr.PerformDeviceCodeFlow(ctx, func(msg string) {
			fmt.Println("\n==========================================")
			fmt.Println(msg)
			fmt.Println("==========================================")
		})
		if err != nil {
			log.Fatalf("Device code authentication failed: %v", err)
		}
		log.Println("Device code authentication completed successfully. Token saved.")
		os.Exit(0)
	}

	// Try acquiring token quietly
	_, err = authMgr.GetAccessToken(ctx)
	if err != nil {
		log.Printf("No valid cached token found. Starting device code authorization...")
		_, err = authMgr.PerformDeviceCodeFlow(ctx, func(msg string) {
			fmt.Println("\n==========================================")
			fmt.Println(msg)
			fmt.Println("==========================================")
		})
		if err != nil {
			log.Fatalf("Authentication required to run proxy: %v", err)
		}
	}

	dbPath := cfg.ResolvedDBPath()
	st, err := store.NewStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize local SQLite store at %s: %v", dbPath, err)
	}
	defer st.Close()

	graphClient := graph.NewClient(authMgr, "")

	imapServer, err := imap.NewServer(cfg, graphClient, st)
	if err != nil {
		log.Fatalf("Failed to initialize IMAP server: %v", err)
	}

	smtpServer, err := smtp.NewServer(cfg, graphClient)
	if err != nil {
		log.Fatalf("Failed to initialize SMTP server: %v", err)
	}

	errChan := make(chan error, 2)
	go func() {
		log.Printf("IMAP server listening on %s", cfg.IMAP.BindAddr)
		if err := imapServer.Start(); err != nil {
			errChan <- fmt.Errorf("IMAP server error: %w", err)
		}
	}()

	go func() {
		log.Printf("SMTP server listening on %s", cfg.SMTP.BindAddr)
		if err := smtpServer.Start(); err != nil {
			errChan <- fmt.Errorf("SMTP server error: %w", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v. Shutting down gracefully...", sig)
	case err := <-errChan:
		log.Printf("Server startup/runtime error: %v", err)
	}

	_ = imapServer.Close()
	_ = smtpServer.Close()
	log.Println("Proxy shut down successfully.")
}
