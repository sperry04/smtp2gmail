// Package message defines the data shared between the SMTP front end
// (package smtpd) and outbound-provider back ends (package gmail): the
// prepared outbound message itself, the Sender interface a provider
// implements, and a small error type providers use to tell the SMTP layer
// whether a failure is worth retrying.
package message

import "context"

// Message is a fully-prepared outbound email -- headers already rewritten
// per policy -- ready to hand to a Sender.
type Message struct {
	// Raw is the complete RFC 5322 message (headers + body), CRLF-terminated
	// lines, ready to be base64url-encoded for the Gmail API's "raw" field
	// or equivalent on another provider.
	Raw []byte
}

// Sender delivers a prepared Message through some outbound provider (Gmail
// API, and in the future others). Implementations should return a
// *SendError so the SMTP layer can map failures to the right response code;
// an unwrapped error is treated as transient (fail-safe: retryable).
type Sender interface {
	Send(ctx context.Context, msg *Message) error
}

// ErrorClass distinguishes failures worth retrying from ones that won't
// succeed no matter how many times the client resends the message.
type ErrorClass int

const (
	// ErrTransient failures (rate limits, upstream 5xx, connectivity) may
	// succeed on a later attempt.
	ErrTransient ErrorClass = iota
	// ErrPermanent failures (invalid recipient, permission denied) will not
	// succeed on retry without a change to the message or configuration.
	ErrPermanent
)

// SendError classifies a Sender failure for the SMTP layer.
type SendError struct {
	Class ErrorClass
	Err   error
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }
