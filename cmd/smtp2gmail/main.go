// Command smtp2gmail runs the SMTP-to-Gmail-API relay sidecar: it accepts
// SMTP connections from sibling containers and relays each accepted message
// through the Gmail API, authenticated as a Google Workspace service
// account with domain-wide delegation.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"smtp2gmail/config"
	"smtp2gmail/gmail"
	"smtp2gmail/smtpd"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	keyBytes, err := os.ReadFile(cfg.GmailKeyFile)
	if err != nil {
		logger.Error("reading service account key file", "path", cfg.GmailKeyFile, "error", err)
		os.Exit(1)
	}

	sender, err := gmail.NewClient(ctx, keyBytes, cfg.GmailSendAs)
	if err != nil {
		logger.Error("initializing gmail client", "error", err)
		os.Exit(1)
	}

	server := smtpd.New(smtpd.Config{
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		SendAs:   cfg.GmailSendAs,
	}, sender, logger)

	logger.Info("starting smtp2gmail", "ports", cfg.ListenPorts, "send_as", cfg.GmailSendAs)
	if err := server.ListenAndServe(ctx, cfg.ListenPorts); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
