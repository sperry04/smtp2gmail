package message

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// BuildResult is the outcome of applying the header-rewrite policy to a raw
// message received over SMTP DATA.
type BuildResult struct {
	Message  *Message
	Warnings []string
}

// Build applies smtp2gmail's header-rewrite policy (see README "Header
// rewrite policy"):
//
//   - From is always rewritten to sendAs, preserving the client's display
//     name if it supplied one. A mismatch between the client's original
//     From and sendAs is logged as a warning, not rejected.
//   - Date and Message-ID are filled in only if missing or invalid;
//     otherwise the client's values are kept as-is.
//   - Bcc, Return-Path and Received are stripped -- these should never be
//     asserted by a client.
//   - To/Cc/Subject/body/custom headers are left untouched.
//
// It also reconciles the SMTP envelope (rcptTo) against the To/Cc headers:
// the Gmail API has no separate "recipients" parameter, it delivers based
// solely on the To/Cc/Bcc headers of the raw message. Any rcptTo address not
// already visible in To/Cc is added to a (re-synthesized) Bcc header, so a
// client that Bcc's someone purely via an extra RCPT TO -- the normal,
// correct way to do it -- doesn't silently have that recipient dropped.
func Build(raw []byte, rcptTo []string, sendAs string) (*BuildResult, error) {
	headerBlock, body := splitMessage(raw)
	fields, err := parseHeaderFields(headerBlock)
	if err != nil {
		return nil, fmt.Errorf("parsing message headers: %w", err)
	}

	var warnings []string

	displayName := ""
	if idx := findField(fields, "From"); idx != -1 {
		if addr, err := mail.ParseAddress(fields[idx].Value); err == nil {
			if !strings.EqualFold(addr.Address, sendAs) {
				warnings = append(warnings, fmt.Sprintf(
					"client From address %q does not match enforced send-as address %q; message was sent as %q",
					addr.Address, sendAs, sendAs))
			}
			displayName = addr.Name
		}
	}
	enforcedFrom := (&mail.Address{Name: displayName, Address: sendAs}).String()
	if idx := findField(fields, "From"); idx != -1 {
		fields[idx].Value = enforcedFrom
	} else {
		fields = append(fields, headerField{Key: "From", Value: enforcedFrom})
	}

	if idx := findField(fields, "Date"); idx != -1 {
		if _, err := mail.ParseDate(fields[idx].Value); err != nil {
			fields[idx].Value = time.Now().UTC().Format(time.RFC1123Z)
		}
	} else {
		fields = append(fields, headerField{Key: "Date", Value: time.Now().UTC().Format(time.RFC1123Z)})
	}

	if findField(fields, "Message-Id") == -1 {
		fields = append(fields, headerField{Key: "Message-ID", Value: generateMessageID(sendAs)})
	}

	for _, key := range []string{"Bcc", "Return-Path", "Received"} {
		fields = removeFields(fields, key)
	}

	visible := map[string]bool{}
	for _, key := range []string{"To", "Cc"} {
		if idx := findField(fields, key); idx != -1 {
			if addrs, err := mail.ParseAddressList(fields[idx].Value); err == nil {
				for _, a := range addrs {
					visible[strings.ToLower(a.Address)] = true
				}
			}
		}
	}
	var bccOnly []string
	for _, rcpt := range rcptTo {
		if !visible[strings.ToLower(rcpt)] {
			bccOnly = append(bccOnly, rcpt)
		}
	}
	if len(bccOnly) > 0 {
		fields = append(fields, headerField{Key: "Bcc", Value: strings.Join(bccOnly, ", ")})
	}

	var buf bytes.Buffer
	buf.Write(serializeHeaderFields(fields))
	buf.WriteString("\r\n")
	buf.Write(body)

	return &BuildResult{
		Message:  &Message{Raw: buf.Bytes()},
		Warnings: warnings,
	}, nil
}

func generateMessageID(sendAs string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := sendAs
	if idx := strings.LastIndex(sendAs, "@"); idx != -1 {
		domain = sendAs[idx+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b), domain)
}
