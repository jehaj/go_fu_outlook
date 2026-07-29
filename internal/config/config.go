package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Azure     AzureConfig     `yaml:"azure"`
	IMAP      ServerConfig    `yaml:"imap"`
	SMTP      ServerConfig    `yaml:"smtp"`
	LocalAuth LocalAuthConfig `yaml:"local_auth"`
	Storage   StorageConfig   `yaml:"storage"`
	Sync      SyncConfig      `yaml:"sync"`
	Logging   LoggingConfig   `yaml:"logging"`
}

type AzureConfig struct {
	TenantID string   `yaml:"tenant_id"`
	ClientID string   `yaml:"client_id"`
	Scopes   []string `yaml:"scopes"`
}

type ServerConfig struct {
	BindAddr string `yaml:"bind_addr"`
}

type LocalAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type StorageConfig struct {
	DataDir   string `yaml:"data_dir"`
	TokenFile string `yaml:"token_file"`
	DBFile    string `yaml:"db_file"`
}

type SyncConfig struct {
	PollInterval string `yaml:"poll_interval"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

const (
	DefaultTenantID       = "organizations"
	DefaultClientID       = "d3590ed6-52b3-4102-aeff-aad2292ab01c" // Microsoft Office first-party app ID
	DefaultIMAPBindAddr   = "127.0.0.1:1143"
	DefaultSMTPBindAddr   = "127.0.0.1:1025"
	DefaultLocalUsername  = "thunderbird"
	DefaultLocalPassword  = "localpassword"
	DefaultDataDir        = "~/.config/graph-mail-proxy"
	DefaultTokenFile      = "tokens.json"
	DefaultDBFile         = "proxy.db"
	DefaultPollInterval   = "1m"
	DefaultLogLevel       = "info"
)

func DefaultConfig() *Config {
	return &Config{
		Azure: AzureConfig{
			TenantID: DefaultTenantID,
			ClientID: DefaultClientID,
			Scopes:   []string{"Mail.ReadWrite", "Mail.Send", "offline_access"},
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

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Azure.TenantID == "" {
		c.Azure.TenantID = DefaultTenantID
	}
	if c.Azure.ClientID == "" {
		c.Azure.ClientID = DefaultClientID
	}
	if len(c.Azure.Scopes) == 0 {
		c.Azure.Scopes = []string{"Mail.ReadWrite", "Mail.Send", "offline_access"}
	}
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
