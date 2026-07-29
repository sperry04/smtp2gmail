package smtpd

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"smtp2gmail/message"
)

// stubSender is a message.Sender test double.
type stubSender struct {
	err       error
	received  []*message.Message
	sendCalls int
}

func (s *stubSender) Send(ctx context.Context, msg *message.Message) error {
	s.sendCalls++
	s.received = append(s.received, msg)
	return s.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testClient wraps one end of a net.Pipe with line read/write helpers and
// starts the server session on the other end in a goroutine.
type testClient struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
}

func newTestSession(t *testing.T, cfg Config, sender message.Sender) *testClient {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv := New(cfg, sender, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		sess := newSession(serverConn, srv)
		sess.run(ctx)
		serverConn.Close()
	}()

	tc := &testClient{t: t, conn: clientConn, reader: bufio.NewReader(clientConn)}
	t.Cleanup(func() { clientConn.Close() })

	tc.expectLine("220") // greeting
	return tc
}

func (tc *testClient) send(line string) {
	tc.t.Helper()
	tc.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := tc.conn.Write([]byte(line + "\r\n")); err != nil {
		tc.t.Fatalf("write %q: %v", line, err)
	}
}

func (tc *testClient) readLine() string {
	tc.t.Helper()
	tc.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := tc.reader.ReadString('\n')
	if err != nil {
		tc.t.Fatalf("read line: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func (tc *testClient) expectLine(prefix string) string {
	tc.t.Helper()
	line := tc.readLine()
	if !strings.HasPrefix(line, prefix) {
		tc.t.Fatalf("expected line starting with %q, got %q", prefix, line)
	}
	return line
}

func (tc *testClient) expectMultiline(prefix string) {
	tc.t.Helper()
	for {
		line := tc.expectLine(prefix)
		// "250-..." continues, "250 ..." (space) is the final line
		if len(line) >= 4 && line[3] == ' ' {
			return
		}
	}
}

func (tc *testClient) authLogin(username, password string) {
	tc.t.Helper()
	tc.send("AUTH LOGIN")
	tc.expectLine("334")
	tc.send(base64.StdEncoding.EncodeToString([]byte(username)))
	tc.expectLine("334")
	tc.send(base64.StdEncoding.EncodeToString([]byte(password)))
}

func TestSMTP_FullTransaction_Success(t *testing.T) {
	sender := &stubSender{}
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "no_reply@urabus.com"}, sender)

	tc.send("EHLO client.example")
	tc.expectMultiline("250")

	tc.authLogin("ghost", "hunter2")
	tc.expectLine("235")

	tc.send("MAIL FROM:<blog@wrong.example>")
	tc.expectLine("250")

	tc.send("RCPT TO:<reader@example.com>")
	tc.expectLine("250")

	tc.send("DATA")
	tc.expectLine("354")
	tc.send("From: blog@wrong.example")
	tc.send("To: reader@example.com")
	tc.send("Subject: Hello")
	tc.send("")
	tc.send("Hello world")
	tc.send(".")
	tc.expectLine("250")

	tc.send("QUIT")
	tc.expectLine("221")

	if sender.sendCalls != 1 {
		t.Fatalf("expected exactly 1 send call, got %d", sender.sendCalls)
	}
	if !strings.Contains(string(sender.received[0].Raw), "no_reply@urabus.com") {
		t.Errorf("expected enforced sender in message, got:\n%s", sender.received[0].Raw)
	}
}

func TestSMTP_RejectsMailBeforeAuth(t *testing.T) {
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, &stubSender{})

	tc.send("EHLO client.example")
	tc.expectMultiline("250")

	tc.send("MAIL FROM:<someone@example.com>")
	tc.expectLine("530")
}

func TestSMTP_RejectsBadCredentials(t *testing.T) {
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, &stubSender{})

	tc.send("EHLO client.example")
	tc.expectMultiline("250")

	tc.authLogin("ghost", "wrong-password")
	tc.expectLine("535")
}

func TestSMTP_RcptBeforeMailRejected(t *testing.T) {
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, &stubSender{})

	tc.send("EHLO client.example")
	tc.expectMultiline("250")
	tc.authLogin("ghost", "hunter2")
	tc.expectLine("235")

	tc.send("RCPT TO:<reader@example.com>")
	tc.expectLine("503")
}

func TestSMTP_SendErrorMapsToTemporaryCode(t *testing.T) {
	sender := &stubSender{err: &message.SendError{Class: message.ErrTransient, Err: errBoom}}
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, sender)

	tc.send("EHLO client.example")
	tc.expectMultiline("250")
	tc.authLogin("ghost", "hunter2")
	tc.expectLine("235")
	tc.send("MAIL FROM:<a@b.com>")
	tc.expectLine("250")
	tc.send("RCPT TO:<reader@example.com>")
	tc.expectLine("250")
	tc.send("DATA")
	tc.expectLine("354")
	tc.send("Subject: test")
	tc.send("")
	tc.send("body")
	tc.send(".")
	tc.expectLine("451")
}

func TestSMTP_SendErrorMapsToPermanentCode(t *testing.T) {
	sender := &stubSender{err: &message.SendError{Class: message.ErrPermanent, Err: errBoom}}
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, sender)

	tc.send("EHLO client.example")
	tc.expectMultiline("250")
	tc.authLogin("ghost", "hunter2")
	tc.expectLine("235")
	tc.send("MAIL FROM:<a@b.com>")
	tc.expectLine("250")
	tc.send("RCPT TO:<reader@example.com>")
	tc.expectLine("250")
	tc.send("DATA")
	tc.expectLine("354")
	tc.send("Subject: test")
	tc.send("")
	tc.send("body")
	tc.send(".")
	tc.expectLine("550")
}

func TestSMTP_DotStuffingUnescaped(t *testing.T) {
	sender := &stubSender{}
	tc := newTestSession(t, Config{Username: "ghost", Password: "hunter2", SendAs: "a@b.com"}, sender)

	tc.send("EHLO client.example")
	tc.expectMultiline("250")
	tc.authLogin("ghost", "hunter2")
	tc.expectLine("235")
	tc.send("MAIL FROM:<a@b.com>")
	tc.expectLine("250")
	tc.send("RCPT TO:<reader@example.com>")
	tc.expectLine("250")
	tc.send("DATA")
	tc.expectLine("354")
	tc.send("Subject: test")
	tc.send("")
	tc.send("..this line starts with a dot")
	tc.send(".")
	tc.expectLine("250")

	if !strings.Contains(string(sender.received[0].Raw), ".this line starts with a dot") {
		t.Errorf("expected dot-stuffing to be undone, got:\n%s", sender.received[0].Raw)
	}
}

var errBoom = &testError{"boom"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
