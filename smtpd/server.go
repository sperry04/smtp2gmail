// Package smtpd implements just enough of RFC 5321/5322 to satisfy typical
// application mail libraries (EHLO, AUTH LOGIN/PLAIN, MAIL FROM, RCPT TO,
// DATA, RSET, NOOP, QUIT). It intentionally never advertises STARTTLS: this
// server is meant to be reachable only from sibling containers on a
// Docker-internal network, never published on a host port -- see the
// project README's "Security considerations" section.
package smtpd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"smtp2gmail/message"
)

// Config is the single, shared configuration every listener uses -- all
// listener ports behave identically in the MVP (see README "Listeners").
type Config struct {
	// Username/Password are the single fixed SMTP AUTH credential pair.
	Username string
	Password string
	// SendAs is the Gmail address every accepted message is enforced to
	// appear from, per the header-rewrite policy in package message.
	SendAs string
}

// Server accepts SMTP connections on one or more ports and hands completed
// messages to Sender.
type Server struct {
	cfg    Config
	sender message.Sender
	log    *slog.Logger
}

// New builds a Server. logger must not be nil.
func New(cfg Config, sender message.Sender, logger *slog.Logger) *Server {
	return &Server{cfg: cfg, sender: sender, log: logger}
}

// ListenAndServe listens on all of ports and serves connections until ctx is
// canceled, at which point all listeners are closed and it returns once
// every in-flight connection handler has returned.
func (s *Server) ListenAndServe(ctx context.Context, ports []int) error {
	var listeners []net.Listener
	for _, port := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			for _, existing := range listeners {
				existing.Close()
			}
			return fmt.Errorf("listening on port %d: %w", port, err)
		}
		s.log.Info("listening", "port", port)
		listeners = append(listeners, l)
	}

	go func() {
		<-ctx.Done()
		for _, l := range listeners {
			l.Close()
		}
	}()

	var wg sync.WaitGroup
	for _, l := range listeners {
		wg.Add(1)
		go func(l net.Listener) {
			defer wg.Done()
			s.acceptLoop(ctx, l)
		}(l)
	}
	wg.Wait()
	return nil
}

func (s *Server) acceptLoop(ctx context.Context, l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.log.Warn("accept error", "error", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sess := newSession(conn, s)
	sess.run(ctx)
}
