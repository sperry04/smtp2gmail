package gmail

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"smtp2gmail/message"
)

// newFakeServiceAccountKey generates a throwaway RSA key and wraps it in a
// service-account JSON blob shaped like a real downloaded key file, so
// google.JWTConfigFromJSON can parse it. No real Google credentials are
// used anywhere in this test.
func newFakeServiceAccountKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling test key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	sa := map[string]string{
		"type":                        "service_account",
		"project_id":                  "test-project",
		"private_key_id":              "test-key-id",
		"private_key":                 string(pemKey),
		"client_email":                "smtp2gmail-sender@test-project.iam.gserviceaccount.com",
		"client_id":                   "000000000000000000000",
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
	}
	b, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshaling fake service account: %v", err)
	}
	return b
}

// fakeTokenServer returns a canned OAuth2 bearer token for any request,
// standing in for Google's real token endpoint.
func fakeTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`)
	}))
}

func newTestClient(t *testing.T, tokenSrv, apiSrv *httptest.Server) *Client {
	t.Helper()
	keyJSON := newFakeServiceAccountKey(t)
	c, err := NewClient(context.Background(), keyJSON, "no_reply@urabus.com",
		WithTokenURL(tokenSrv.URL), WithAPIBase(apiSrv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestSend_Success(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	defer tokenSrv.Close()

	var capturedAuth string
	var capturedBody map[string]string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/gmail/v1/users/me/messages/send" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"123"}`)
	}))
	defer apiSrv.Close()

	client := newTestClient(t, tokenSrv, apiSrv)
	err := client.Send(context.Background(), &message.Message{Raw: []byte("From: a@b.com\r\n\r\nbody")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if capturedAuth != "Bearer fake-token" {
		t.Errorf("expected Authorization header with fake token, got %q", capturedAuth)
	}
	if _, ok := capturedBody["raw"]; !ok {
		t.Errorf("expected request body to contain a 'raw' field, got %v", capturedBody)
	}
}

func TestSend_TransientErrorOnRateLimit(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer apiSrv.Close()

	client := newTestClient(t, tokenSrv, apiSrv)
	err := client.Send(context.Background(), &message.Message{Raw: []byte("body")})

	var sendErr *message.SendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("expected a *message.SendError, got %T: %v", err, err)
	}
	if sendErr.Class != message.ErrTransient {
		t.Errorf("expected ErrTransient for 429, got %v", sendErr.Class)
	}
}

func TestSend_TransientErrorOnServerError(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"internal"}`)
	}))
	defer apiSrv.Close()

	client := newTestClient(t, tokenSrv, apiSrv)
	err := client.Send(context.Background(), &message.Message{Raw: []byte("body")})

	var sendErr *message.SendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("expected a *message.SendError, got %T: %v", err, err)
	}
	if sendErr.Class != message.ErrTransient {
		t.Errorf("expected ErrTransient for 500, got %v", sendErr.Class)
	}
}

func TestSend_PermanentErrorOnBadRequest(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid recipient"}`)
	}))
	defer apiSrv.Close()

	client := newTestClient(t, tokenSrv, apiSrv)
	err := client.Send(context.Background(), &message.Message{Raw: []byte("body")})

	var sendErr *message.SendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("expected a *message.SendError, got %T: %v", err, err)
	}
	if sendErr.Class != message.ErrPermanent {
		t.Errorf("expected ErrPermanent for 400, got %v", sendErr.Class)
	}
}

func TestSend_PermanentErrorOnForbidden(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"permission denied"}`)
	}))
	defer apiSrv.Close()

	client := newTestClient(t, tokenSrv, apiSrv)
	err := client.Send(context.Background(), &message.Message{Raw: []byte("body")})

	var sendErr *message.SendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("expected a *message.SendError, got %T: %v", err, err)
	}
	if sendErr.Class != message.ErrPermanent {
		t.Errorf("expected ErrPermanent for 403, got %v", sendErr.Class)
	}
}
