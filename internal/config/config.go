package config

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Azure     AzureConfig
	IMAP      ServerConfig
	SMTP      ServerConfig
	LocalAuth LocalAuthConfig
	Storage   StorageConfig
	Sync      SyncConfig
	Logging   LoggingConfig
}

type AzureConfig struct {
	TenantID string
	ClientID string
	Scopes   []string
}

type ServerConfig struct {
	BindAddr string
}

type LocalAuthConfig struct {
	Username string
	Password string
}

type StorageConfig struct {
	DataDir   string
	TokenFile string
	DBFile    string
}

type SyncConfig struct {
	PollInterval string
}

type LoggingConfig struct {
	Level string
}

const (
	DefaultTenantID     = "organizations"
	DefaultClientID     = "d3590ed6-52b3-4102-aeff-aad2292ab01c" // Microsoft Office first-party app ID
	DefaultIMAPBindAddr = "127.0.0.1:1143"
	DefaultSMTPBindAddr = "127.0.0.1:1025"
	DefaultLocalUsername = "thunderbird"
	DefaultLocalPassword = "localpassword"
	DefaultDataDir      = "~/.config/graph-mail-proxy"
	DefaultTokenFile    = "tokens.json"
	DefaultDBFile       = "proxy.db"
	DefaultPollInterval = "1m"
	DefaultLogLevel     = "info"
)

func DefaultConfig() *Config {
	return &Config{
		Azure: AzureConfig{
			TenantID: DefaultTenantID,
			ClientID: DefaultClientID,
			Scopes:   []string{"Mail.ReadWrite", "Mail.Send"},
		},
		IMAP: ServerConfig{
			BindAddr: DefaultIMAPBindAddr,
		},
		SMTP: ServerConfig{
			BindAddr: DefaultSMTPBindAddr,
		},
		LocalAuth: LocalAuthConfig{
			Username: DefaultLocalUsername,
			Password: DefaultLocalPassword,
		},
		Storage: StorageConfig{
			DataDir:   DefaultDataDir,
			TokenFile: DefaultTokenFile,
			DBFile:    DefaultDBFile,
		},
		Sync: SyncConfig{
			PollInterval: DefaultPollInterval,
		},
		Logging: LoggingConfig{
			Level: DefaultLogLevel,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, cfg.Validate()
	}

	expandedPath := ExpandPath(path)
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found at %s", expandedPath)
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := parseYAMLConfig(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func parseYAMLConfig(content []byte, cfg *Config) error {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	var currentSection string
	var currentList *[]string

	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "-") && currentList != nil {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			val = strings.Trim(val, "\"'")
			if val != "" {
				*currentList = append(*currentList, val)
			}
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"'")

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 && val == "" {
			currentSection = key
			currentList = nil
			continue
		}

		fullKey := key
		if indent > 0 && currentSection != "" {
			fullKey = currentSection + "." + key
		}

		switch fullKey {
		case "azure.tenant_id":
			if val != "" {
				cfg.Azure.TenantID = val
			}
		case "azure.client_id":
			if val != "" {
				cfg.Azure.ClientID = val
			}
		case "azure.scopes":
			if val != "" {
				cfg.Azure.Scopes = append(cfg.Azure.Scopes, val)
			} else {
				cfg.Azure.Scopes = nil
				currentList = &cfg.Azure.Scopes
			}
		case "imap.bind_addr":
			if val != "" {
				cfg.IMAP.BindAddr = val
			}
		case "smtp.bind_addr":
			if val != "" {
				cfg.SMTP.BindAddr = val
			}
		case "local_auth.username":
			if val != "" {
				cfg.LocalAuth.Username = val
			}
		case "local_auth.password":
			if val != "" {
				cfg.LocalAuth.Password = val
			}
		case "storage.data_dir":
			if val != "" {
				cfg.Storage.DataDir = val
			}
		case "storage.token_file":
			if val != "" {
				cfg.Storage.TokenFile = val
			}
		case "storage.db_file":
			if val != "" {
				cfg.Storage.DBFile = val
			}
		case "sync.poll_interval":
			if val != "" {
				cfg.Sync.PollInterval = val
			}
		case "logging.level":
			if val != "" {
				cfg.Logging.Level = val
			}
		}
	}
	return scanner.Err()
}

func (c *Config) Validate() error {
	if c.Azure.TenantID == "" {
		c.Azure.TenantID = DefaultTenantID
	}
	if c.Azure.ClientID == "" {
		c.Azure.ClientID = DefaultClientID
	}

	// Filter out offline_access and openid from requested scopes because MSAL requests them automatically
	// and explicit inclusion causes MSAL scope validation to fail with "declined scopes are present: offline_access"
	var filteredScopes []string
	for _, s := range c.Azure.Scopes {
		sTrim := strings.TrimSpace(s)
		if !strings.EqualFold(sTrim, "offline_access") && !strings.EqualFold(sTrim, "openid") && sTrim != "" {
			filteredScopes = append(filteredScopes, sTrim)
		}
	}
	if len(filteredScopes) == 0 {
		filteredScopes = []string{"Mail.ReadWrite", "Mail.Send"}
	}
	c.Azure.Scopes = filteredScopes

	if c.IMAP.BindAddr == "" {
		c.IMAP.BindAddr = DefaultIMAPBindAddr
	}
	if c.SMTP.BindAddr == "" {
		c.SMTP.BindAddr = DefaultSMTPBindAddr
	}
	if c.Storage.DataDir == "" {
		c.Storage.DataDir = DefaultDataDir
	}
	if c.Storage.TokenFile == "" {
		c.Storage.TokenFile = DefaultTokenFile
	}
	if c.Storage.DBFile == "" {
		c.Storage.DBFile = DefaultDBFile
	}
	if c.Sync.PollInterval == "" {
		c.Sync.PollInterval = DefaultPollInterval
	}
	if _, err := time.ParseDuration(c.Sync.PollInterval); err != nil {
		return fmt.Errorf("invalid poll interval duration %q: %w", c.Sync.PollInterval, err)
	}
	if c.Logging.Level == "" {
		c.Logging.Level = DefaultLogLevel
	}

	if err := validateLoopbackAddr("IMAP", c.IMAP.BindAddr); err != nil {
		return err
	}
	if err := validateLoopbackAddr("SMTP", c.SMTP.BindAddr); err != nil {
		return err
	}

	return nil
}

func validateLoopbackAddr(serverType, addrStr string) error {
	host, _, err := net.SplitHostPort(addrStr)
	if err != nil {
		host = addrStr
	}

	if strings.EqualFold(host, "localhost") {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("security violation: %s bind address %q host %q is not a valid IP or 'localhost'", serverType, addrStr, host)
	}

	if !ip.IsLoopback() {
		return fmt.Errorf("security violation: %s bind address %q is not a loopback address (must be 127.0.0.1 or ::1)", serverType, addrStr)
	}

	return nil
}

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (c *Config) ResolvedTokenPath() string {
	dataDir := ExpandPath(c.Storage.DataDir)
	return filepath.Join(dataDir, c.Storage.TokenFile)
}

func (c *Config) ResolvedDBPath() string {
	dataDir := ExpandPath(c.Storage.DataDir)
	return filepath.Join(dataDir, c.Storage.DBFile)
}
