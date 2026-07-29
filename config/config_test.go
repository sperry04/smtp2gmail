package config

import (
	"reflect"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoad_Success(t *testing.T) {
	env := envFrom(map[string]string{
		"SMTP_LISTEN_PORTS": "25, 587",
		"SMTP_USERNAME":     "ghost",
		"SMTP_PASSWORD":     "hunter2",
		"GMAIL_SEND_AS":     "no_reply@urabus.com",
		"GMAIL_SA_KEY_FILE": "/secrets/sa.json",
	})

	cfg, err := load(env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg.ListenPorts, []int{25, 587}) {
		t.Errorf("ListenPorts = %v, want [25 587]", cfg.ListenPorts)
	}
	if cfg.SMTPUsername != "ghost" || cfg.SMTPPassword != "hunter2" {
		t.Errorf("unexpected SMTP credentials: %+v", cfg)
	}
	if cfg.GmailSendAs != "no_reply@urabus.com" || cfg.GmailKeyFile != "/secrets/sa.json" {
		t.Errorf("unexpected Gmail config: %+v", cfg)
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	base := map[string]string{
		"SMTP_LISTEN_PORTS": "587",
		"SMTP_USERNAME":     "ghost",
		"SMTP_PASSWORD":     "hunter2",
		"GMAIL_SEND_AS":     "no_reply@urabus.com",
		"GMAIL_SA_KEY_FILE": "/secrets/sa.json",
	}

	for _, missing := range []string{
		"SMTP_LISTEN_PORTS", "SMTP_USERNAME", "SMTP_PASSWORD", "GMAIL_SEND_AS", "GMAIL_SA_KEY_FILE",
	} {
		t.Run("missing_"+missing, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range base {
				if k != missing {
					env[k] = v
				}
			}
			if _, err := load(envFrom(env)); err == nil {
				t.Errorf("expected error when %s is missing, got nil", missing)
			}
		})
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	env := envFrom(map[string]string{
		"SMTP_LISTEN_PORTS": "25,not-a-port",
		"SMTP_USERNAME":     "ghost",
		"SMTP_PASSWORD":     "hunter2",
		"GMAIL_SEND_AS":     "no_reply@urabus.com",
		"GMAIL_SA_KEY_FILE": "/secrets/sa.json",
	})
	if _, err := load(env); err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}

func TestLoad_PortOutOfRange(t *testing.T) {
	env := envFrom(map[string]string{
		"SMTP_LISTEN_PORTS": "70000",
		"SMTP_USERNAME":     "ghost",
		"SMTP_PASSWORD":     "hunter2",
		"GMAIL_SEND_AS":     "no_reply@urabus.com",
		"GMAIL_SA_KEY_FILE": "/secrets/sa.json",
	})
	if _, err := load(env); err == nil {
		t.Error("expected error for out-of-range port, got nil")
	}
}
