// Package config loads smtp2gmail's runtime configuration from environment
// variables, per the MVP decision to keep configuration 12-factor/env-var
// driven rather than requiring a config file.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is smtp2gmail's full runtime configuration for the MVP: a single
// SMTP credential pair, a single Gmail send-as identity, and the listener
// ports -- all listeners share this same configuration.
type Config struct {
	ListenPorts  []int
	SMTPUsername string
	SMTPPassword string
	GmailSendAs  string
	GmailKeyFile string
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	return load(os.Getenv)
}

// load is factored out from Load so tests can inject a fake environment
// without mutating real process-wide env vars.
func load(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		SMTPUsername: getenv("SMTP_USERNAME"),
		SMTPPassword: getenv("SMTP_PASSWORD"),
		GmailSendAs:  getenv("GMAIL_SEND_AS"),
		GmailKeyFile: getenv("GMAIL_SA_KEY_FILE"),
	}

	portsRaw := getenv("SMTP_LISTEN_PORTS")
	if portsRaw == "" {
		return nil, fmt.Errorf(`SMTP_LISTEN_PORTS is required (e.g. "25,587")`)
	}
	for _, p := range strings.Split(portsRaw, ",") {
		p = strings.TrimSpace(p)
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid port %q in SMTP_LISTEN_PORTS", p)
		}
		cfg.ListenPorts = append(cfg.ListenPorts, port)
	}

	if cfg.SMTPUsername == "" {
		return nil, fmt.Errorf("SMTP_USERNAME is required")
	}
	if cfg.SMTPPassword == "" {
		return nil, fmt.Errorf("SMTP_PASSWORD is required")
	}
	if cfg.GmailSendAs == "" {
		return nil, fmt.Errorf("GMAIL_SEND_AS is required")
	}
	if cfg.GmailKeyFile == "" {
		return nil, fmt.Errorf("GMAIL_SA_KEY_FILE is required")
	}

	return cfg, nil
}
