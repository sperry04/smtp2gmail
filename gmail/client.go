// Package gmail sends prepared messages through the Gmail API, authenticated
// as a Google Workspace service account with domain-wide delegation. It
// deliberately talks to Gmail's REST endpoint directly over net/http rather
// than depending on the full generated google.golang.org/api/gmail/v1
// client -- that client pulls in a much larger dependency tree for what is,
// underneath, a single simple POST request, and "lightweight" is a stated
// project goal.
package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2/google"

	"smtp2gmail/message"
)

const sendScope = "https://www.googleapis.com/auth/gmail.send"

const defaultAPIBase = "https://gmail.googleapis.com"

// Client sends messages via the Gmail API, impersonating the Workspace
// address it was constructed with via domain-wide delegation.
type Client struct {
	httpClient *http.Client
	apiBase    string
}

// Option customizes Client construction; used by tests to redirect the
// token endpoint and API base at httptest servers instead of real Google
// infrastructure.
type Option func(*options)

type options struct {
	tokenURL string
	apiBase  string
}

// WithTokenURL overrides the OAuth2 token endpoint the service account JWT
// is exchanged against. Intended for tests.
func WithTokenURL(url string) Option {
	return func(o *options) { o.tokenURL = url }
}

// WithAPIBase overrides the Gmail API base URL. Intended for tests.
func WithAPIBase(url string) Option {
	return func(o *options) { o.apiBase = url }
}

// NewClient builds a Client authenticated as the service account described
// by keyJSON (the downloaded service-account JSON key file contents),
// impersonating sendAs via domain-wide delegation's subject claim.
func NewClient(ctx context.Context, keyJSON []byte, sendAs string, opts ...Option) (*Client, error) {
	o := options{apiBase: defaultAPIBase}
	for _, opt := range opts {
		opt(&o)
	}

	jwtConfig, err := google.JWTConfigFromJSON(keyJSON, sendScope)
	if err != nil {
		return nil, fmt.Errorf("parsing service account key: %w", err)
	}
	jwtConfig.Subject = sendAs
	if o.tokenURL != "" {
		jwtConfig.TokenURL = o.tokenURL
	}

	return &Client{
		httpClient: jwtConfig.Client(ctx),
		apiBase:    o.apiBase,
	}, nil
}

// Send implements message.Sender by POSTing the message to Gmail's
// users.messages.send endpoint as the impersonated user ("me").
func (c *Client) Send(ctx context.Context, msg *message.Message) error {
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(msg.Raw)
	body, err := json.Marshal(map[string]string{"raw": encoded})
	if err != nil {
		return fmt.Errorf("encoding gmail request body: %w", err)
	}

	url := c.apiBase + "/gmail/v1/users/me/messages/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building gmail request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &message.SendError{Class: message.ErrTransient, Err: fmt.Errorf("calling gmail api: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return classifyError(resp.StatusCode, respBody)
}

// classifyError maps a Gmail API HTTP error to a message.SendError so the
// SMTP layer can respond with the right class of SMTP status code: rate
// limits and server-side errors are transient (client may retry), anything
// else (bad request, permission denied, not found) is treated as permanent.
func classifyError(status int, body []byte) error {
	err := fmt.Errorf("gmail api error (status %d): %s", status, string(body))
	switch {
	case status == http.StatusTooManyRequests, status >= 500:
		return &message.SendError{Class: message.ErrTransient, Err: err}
	default:
		return &message.SendError{Class: message.ErrPermanent, Err: err}
	}
}
