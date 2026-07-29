package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"graph-mail-proxy/internal/config"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

var (
	ErrNoValidToken   = errors.New("no valid token found in cache; interactive authentication required")
	ErrAuthFailed     = errors.New("authentication failed")
)

type AuthManager struct {
	cfg        *config.Config
	msalClient public.Client
	tokenCache *FileTokenCache
}

func NewAuthManager(cfg *config.Config) (*AuthManager, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}

	authority := fmt.Sprintf("https://login.microsoftonline.com/%s", cfg.Azure.TenantID)
	tokenPath := cfg.ResolvedTokenPath()
	tokenCache := NewFileTokenCache(tokenPath)

	client, err := public.New(
		cfg.Azure.ClientID,
		public.WithAuthority(authority),
		public.WithCache(tokenCache),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MSAL public client: %w", err)
	}

	return &AuthManager{
		cfg:        cfg,
		msalClient: client,
		tokenCache: tokenCache,
	}, nil
}

// GetAccessToken attempts silent token acquisition using cached tokens.
func (a *AuthManager) GetAccessToken(ctx context.Context) (string, error) {
	accounts, err := a.msalClient.Accounts(ctx)
	if err != nil || len(accounts) == 0 {
		return "", ErrNoValidToken
	}

	result, err := a.msalClient.AcquireTokenSilent(
		ctx,
		a.cfg.Azure.Scopes,
		public.WithSilentAccount(accounts[0]),
	)
	if err != nil {
		return "", fmt.Errorf("%w: silent token acquisition failed: %v", ErrNoValidToken, err)
	}

	return result.AccessToken, nil
}

// PerformDeviceCodeFlow executes interactive device code authentication.
func (a *AuthManager) PerformDeviceCodeFlow(ctx context.Context, userPromptCallback func(message string)) (string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	deviceCode, err := a.msalClient.AcquireTokenByDeviceCode(ctxWithTimeout, a.cfg.Azure.Scopes)
	if err != nil {
		return "", fmt.Errorf("failed to start device code flow: %w", err)
	}

	if userPromptCallback != nil {
		userPromptCallback(deviceCode.Result.Message)
	}

	result, err := deviceCode.AuthenticationResult(ctxWithTimeout)
	if err != nil {
		return "", fmt.Errorf("device code authentication failed: %w", err)
	}

	return result.AccessToken, nil
}

func (a *AuthManager) TokenFilePath() string {
	return a.tokenCache.FilePath()
}
