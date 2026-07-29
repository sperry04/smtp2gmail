package message

import (
	"strings"
	"testing"
)

func TestBuild_FromAlwaysEnforced(t *testing.T) {
	raw := []byte("From: \"Blog\" <wrong@other.com>\r\nTo: reader@example.com\r\nSubject: Hi\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"reader@example.com"}, "no_reply@urabus.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	msg := string(result.Message.Raw)
	if !strings.Contains(msg, `From: "Blog" <no_reply@urabus.com>`) {
		t.Errorf("expected rewritten From with preserved display name, got:\n%s", msg)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("expected 1 mismatch warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
}

func TestBuild_FromMissing(t *testing.T) {
	raw := []byte("To: reader@example.com\r\nSubject: Hi\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"reader@example.com"}, "no_reply@urabus.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	msg := string(result.Message.Raw)
	if !strings.Contains(msg, "From: <no_reply@urabus.com>") {
		t.Errorf("expected inserted From header, got:\n%s", msg)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings when From was simply absent, got %v", result.Warnings)
	}
}

func TestBuild_FromMatchesNoWarning(t *testing.T) {
	raw := []byte("From: no_reply@urabus.com\r\nTo: reader@example.com\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"reader@example.com"}, "no_reply@urabus.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings when From already matches, got %v", result.Warnings)
	}
}

func TestBuild_DateFilledWhenMissing(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(result.Message.Raw), "Date: ") {
		t.Errorf("expected a Date header to be added, got:\n%s", result.Message.Raw)
	}
}

func TestBuild_DateFilledWhenInvalid(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nDate: not-a-date\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(result.Message.Raw), "Date: not-a-date") {
		t.Errorf("expected invalid Date to be replaced, got:\n%s", result.Message.Raw)
	}
}

func TestBuild_DatePreservedWhenValid(t *testing.T) {
	const validDate = "Tue, 28 Jul 2026 12:00:00 +0000"
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nDate: " + validDate + "\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(result.Message.Raw), "Date: "+validDate) {
		t.Errorf("expected original valid Date to be preserved, got:\n%s", result.Message.Raw)
	}
}

func TestBuild_MessageIDFilledOnlyIfMissing(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nMessage-ID: <original@client>\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(result.Message.Raw), "Message-ID: <original@client>") {
		t.Errorf("expected client Message-ID to be preserved, got:\n%s", result.Message.Raw)
	}

	raw2 := []byte("From: a@b.com\r\nTo: c@d.com\r\n\r\nbody\r\n")
	result2, err := Build(raw2, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(result2.Message.Raw), "Message-ID: <") {
		t.Errorf("expected a generated Message-ID, got:\n%s", result2.Message.Raw)
	}
}

func TestBuild_StripsDisallowedHeaders(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nBcc: secret@d.com\r\nReturn-Path: <bounce@somewhere>\r\nReceived: from evil.example\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := string(result.Message.Raw)
	for _, disallowed := range []string{"Return-Path:", "Received:"} {
		if strings.Contains(msg, disallowed) {
			t.Errorf("expected %s to be stripped, got:\n%s", disallowed, msg)
		}
	}
	if strings.Contains(msg, "secret@d.com") {
		t.Errorf("expected client-supplied Bcc value to be discarded, got:\n%s", msg)
	}
}

func TestBuild_EnvelopeOnlyRecipientBecomesBcc(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: visible@d.com\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"visible@d.com", "hidden@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := string(result.Message.Raw)
	if !strings.Contains(msg, "Bcc: hidden@d.com") {
		t.Errorf("expected envelope-only recipient to be added as Bcc, got:\n%s", msg)
	}
	if strings.Contains(msg, "visible@d.com") == false {
		t.Errorf("expected visible recipient to remain in To, got:\n%s", msg)
	}
	// visible@d.com must NOT also appear in Bcc
	if strings.Contains(msg, "Bcc: visible") {
		t.Errorf("visible recipient should not be duplicated into Bcc, got:\n%s", msg)
	}
}

func TestBuild_NoBccHeaderWhenNoEnvelopeOnlyRecipients(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: visible@d.com\r\n\r\nbody\r\n")

	result, err := Build(raw, []string{"visible@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(result.Message.Raw), "Bcc:") {
		t.Errorf("expected no Bcc header when all recipients are already visible, got:\n%s", result.Message.Raw)
	}
}

func TestBuild_BodyUntouched(t *testing.T) {
	body := "line one\r\nline two\r\n.escaped-looking line\r\n"
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\n\r\n" + body)

	result, err := Build(raw, []string{"c@d.com"}, "a@b.com")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.HasSuffix(string(result.Message.Raw), body) {
		t.Errorf("expected body to be preserved verbatim, got:\n%s", result.Message.Raw)
	}
}
