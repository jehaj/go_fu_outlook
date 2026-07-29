package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

var defaultClientIDs = map[string]string{
	"office":     "d3590ed6-52b3-4102-aeff-aad2292ab01c", // Microsoft Office
	"teams":      "1fec8e78-bce4-4aaf-ab1b-5451cc387264", // Microsoft Teams
	"azure-cli":  "04b07795-8ddb-461a-bbee-02f9e1bf7b46", // Azure CLI
	"azure-ps":   "1950a258-227b-4e31-a9cf-717495945fc2", // Azure PowerShell
}

func main() {
	tenantID := flag.String("tenant", "organizations", "Azure AD Tenant ID or 'organizations'/'common'")
	clientIDFlag := flag.String("client-id", "", "Azure AD Application (Client) ID or alias (office, teams, azure-cli, azure-ps)")
	scopesFlag := flag.String("scopes", "Mail.ReadWrite,Mail.Send,offline_access", "Comma-separated scopes")
	flag.Parse()

	clientID := *clientIDFlag
	if clientID == "" {
		fmt.Println("No -client-id provided. Defaulting to 'office' (d3590ed6-52b3-4102-aeff-aad2292ab01c).")
		fmt.Println("Available aliases: office, teams, azure-cli, azure-ps")
		clientID = defaultClientIDs["office"]
	} else if resolved, ok := defaultClientIDs[strings.ToLower(clientID)]; ok {
		clientID = resolved
	}

	scopes := strings.Split(*scopesFlag, ",")
	for i, s := range scopes {
		scopes[i] = strings.TrimSpace(s)
	}

	authority := fmt.Sprintf("https://login.microsoftonline.com/%s", *tenantID)
	fmt.Printf("=== Azure AD Device Code Auth Spike ===\n")
	fmt.Printf("Authority: %s\n", authority)
	fmt.Printf("Client ID: %s\n", clientID)
	fmt.Printf("Scopes:    %v\n", scopes)
	fmt.Println("=======================================")

	app, err := public.New(clientID, public.WithAuthority(authority))
	if err != nil {
		log.Fatalf("Failed to create MSAL public client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	deviceCode, err := app.AcquireTokenByDeviceCode(ctx, scopes)
	if err != nil {
		log.Fatalf("Failed to initiate device code flow: %v", err)
	}

	fmt.Println("\n--- INSTRUCTIONS ---")
	fmt.Println(deviceCode.Result.Message)
	fmt.Println("--------------------")
	fmt.Println("Waiting for user authentication in browser...")

	result, err := deviceCode.AuthenticationResult(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[ERROR] Authentication failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Inspect the error code (e.g. AADSTS...) to determine if consent, scope, or tenant allow-list policies blocked authorization.\n")
		os.Exit(1)
	}

	fmt.Println("\n[SUCCESS] Authentication succeeded!")
	fmt.Printf("Account:      %s\n", result.Account.PreferredUsername)
	fmt.Printf("Granted Scopes: %v\n", result.GrantedScopes)
	fmt.Printf("Token Expires:  %s\n", result.ExpiresOn.Format(time.RFC3339))
	if len(result.AccessToken) > 20 {
		fmt.Printf("Access Token:   %s...\n", result.AccessToken[:20])
	}
}
