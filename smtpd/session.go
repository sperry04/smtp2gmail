package smtpd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"strings"

	"smtp2gmail/message"
)

type session struct {
	conn          net.Conn
	reader        *bufio.Reader
	server        *Server
	authenticated bool
	from          string
	rcptTo        []string
	remoteAddr    string
}

func newSession(conn net.Conn, server *Server) *session {
	return &session{
		conn:       conn,
		reader:     bufio.NewReaderSize(conn, 64*1024),
		server:     server,
		remoteAddr: conn.RemoteAddr().String(),
	}
}

func (sess *session) run(ctx context.Context) {
	sess.writeLine("220 smtp2gmail ESMTP")
	for {
		line, err := sess.readLine()
		if err != nil {
			return
		}
		cmd, arg := parseCommand(line)
		switch strings.ToUpper(cmd) {
		case "EHLO", "HELO":
			sess.handleHelo()
		case "AUTH":
			sess.handleAuth(arg)
		case "MAIL":
			sess.handleMail(arg)
		case "RCPT":
			sess.handleRcpt(arg)
		case "DATA":
			sess.handleData(ctx)
		case "RSET":
			sess.reset()
			sess.writeLine("250 OK")
		case "NOOP":
			sess.writeLine("250 OK")
		case "QUIT":
			sess.writeLine("221 Bye")
			return
		default:
			sess.writeLine("500 Command not recognized")
		}
	}
}

func (sess *session) readLine() (string, error) {
	line, err := sess.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (sess *session) writeLine(s string) {
	sess.conn.Write([]byte(s + "\r\n"))
}

func parseCommand(line string) (cmd, arg string) {
	parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
	cmd = parts[0]
	if len(parts) > 1 {
		arg = parts[1]
	}
	return cmd, arg
}

func (sess *session) handleHelo() {
	sess.writeLine("250-smtp2gmail")
	sess.writeLine("250 AUTH LOGIN PLAIN")
}

func (sess *session) reset() {
	sess.from = ""
	sess.rcptTo = nil
}

// --- AUTH ---

func (sess *session) handleAuth(arg string) {
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		sess.writeLine("501 Syntax error in parameters")
		return
	}
	mechanism := strings.ToUpper(parts[0])
	switch mechanism {
	case "LOGIN":
		sess.authLogin()
	case "PLAIN":
		var initial string
		if len(parts) > 1 {
			initial = parts[1]
		}
		sess.authPlain(initial)
	default:
		sess.writeLine("504 Unrecognized authentication mechanism")
	}
}

func (sess *session) authLogin() {
	sess.writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
	userB64, err := sess.readLine()
	if err != nil {
		return
	}
	username, err := base64.StdEncoding.DecodeString(userB64)
	if err != nil {
		sess.writeLine("501 Malformed base64")
		return
	}

	sess.writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
	passB64, err := sess.readLine()
	if err != nil {
		return
	}
	password, err := base64.StdEncoding.DecodeString(passB64)
	if err != nil {
		sess.writeLine("501 Malformed base64")
		return
	}

	sess.finishAuth(string(username), string(password))
}

func (sess *session) authPlain(initial string) {
	blob := initial
	if blob == "" {
		sess.writeLine("334 ")
		line, err := sess.readLine()
		if err != nil {
			return
		}
		blob = line
	}
	decoded, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		sess.writeLine("501 Malformed base64")
		return
	}
	// AUTH PLAIN payload format: authzid \0 authcid \0 password
	parts := bytes.SplitN(decoded, []byte{0}, 3)
	if len(parts) != 3 {
		sess.writeLine("501 Malformed AUTH PLAIN response")
		return
	}
	sess.finishAuth(string(parts[1]), string(parts[2]))
}

func (sess *session) finishAuth(username, password string) {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(sess.server.cfg.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(sess.server.cfg.Password)) == 1
	if userOK && passOK {
		sess.authenticated = true
		sess.writeLine("235 2.7.0 Authentication successful")
		return
	}
	sess.server.log.Warn("authentication failed", "remote_addr", sess.remoteAddr)
	sess.writeLine("535 5.7.8 Authentication failed")
}

// --- envelope ---

func (sess *session) handleMail(arg string) {
	if !sess.authenticated {
		sess.writeLine("530 5.7.0 Authentication required")
		return
	}
	addr, ok := parseAngleAddr(arg, "FROM:")
	if !ok {
		sess.writeLine("501 Syntax error in parameters")
		return
	}
	sess.reset()
	sess.from = addr
	sess.writeLine("250 OK")
}

func (sess *session) handleRcpt(arg string) {
	if !sess.authenticated {
		sess.writeLine("530 5.7.0 Authentication required")
		return
	}
	if sess.from == "" {
		sess.writeLine("503 5.5.1 MAIL FROM required before RCPT TO")
		return
	}
	addr, ok := parseAngleAddr(arg, "TO:")
	if !ok {
		sess.writeLine("501 Syntax error in parameters")
		return
	}
	sess.rcptTo = append(sess.rcptTo, addr)
	sess.writeLine("250 OK")
}

func parseAngleAddr(arg, prefix string) (string, bool) {
	if !strings.HasPrefix(strings.ToUpper(arg), prefix) {
		return "", false
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	start := strings.Index(rest, "<")
	end := strings.Index(rest, ">")
	if start == -1 || end == -1 || end < start {
		return "", false
	}
	addr := strings.TrimSpace(rest[start+1 : end])
	if addr == "" {
		return "", false
	}
	return addr, true
}

// --- DATA ---

func (sess *session) handleData(ctx context.Context) {
	if !sess.authenticated {
		sess.writeLine("530 5.7.0 Authentication required")
		return
	}
	if sess.from == "" || len(sess.rcptTo) == 0 {
		sess.writeLine("503 5.5.1 MAIL FROM and RCPT TO required before DATA")
		return
	}

	sess.writeLine("354 Start mail input; end with <CRLF>.<CRLF>")
	raw, err := sess.readDataLines()
	if err != nil {
		sess.server.log.Warn("error reading DATA", "error", err)
		return
	}

	result, err := message.Build(raw, sess.rcptTo, sess.server.cfg.SendAs)
	if err != nil {
		sess.server.log.Warn("rejecting malformed message", "error", err)
		sess.writeLine("554 5.6.0 Message content rejected: could not parse message")
		sess.reset()
		return
	}
	for _, w := range result.Warnings {
		sess.server.log.Warn(w)
	}

	if err := sess.server.sender.Send(ctx, result.Message); err != nil {
		sess.server.log.Error("send failed", "error", err)
		sess.writeLine(codeForError(err))
		sess.reset()
		return
	}

	sess.writeLine("250 2.0.0 OK: message sent")
	sess.reset()
}

// readDataLines reads DATA content up to the terminating "." line,
// reversing SMTP dot-stuffing (RFC 5321 4.5.2): a line beginning with ".."
// has one leading dot removed.
func (sess *session) readDataLines() ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := sess.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			break
		}
		if strings.HasPrefix(trimmed, "..") {
			trimmed = trimmed[1:]
		}
		buf.WriteString(trimmed)
		buf.WriteString("\r\n")
	}
	return buf.Bytes(), nil
}

// codeForError maps a Sender error to an SMTP response line. Unclassified
// errors fail safe as transient/retryable rather than permanently bouncing
// the message on an error type the server doesn't recognize.
func codeForError(err error) string {
	var sendErr *message.SendError
	if errors.As(err, &sendErr) && sendErr.Class == message.ErrPermanent {
		return "550 5.1.1 Message rejected by upstream provider"
	}
	return "451 4.3.0 Temporary failure sending message, please retry later"
}
