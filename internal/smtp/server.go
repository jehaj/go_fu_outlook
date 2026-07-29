package smtp

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"graph-mail-proxy/internal/config"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

type MailSender interface {
	SendMail(ctx context.Context, rawMIME []byte) error
}

type Server struct {
	cfg        *config.Config
	mailSender MailSender
	server     *smtp.Server
	listener   net.Listener
	mu         sync.Mutex
}

func NewServer(cfg *config.Config, mailSender MailSender) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	s := &Server{
		cfg:        cfg,
		mailSender: mailSender,
	}

	smtpServer := smtp.NewServer(s)
	smtpServer.Addr = cfg.SMTP.BindAddr
	smtpServer.Domain = "localhost"
	smtpServer.AllowInsecureAuth = true
	smtpServer.ReadTimeout = 30 * time.Second
	smtpServer.WriteTimeout = 30 * time.Second
	smtpServer.MaxMessageBytes = 25 * 1024 * 1024 // 25 MB max

	s.server = smtpServer
	return s, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	l, err := net.Listen("tcp", s.cfg.SMTP.BindAddr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on SMTP bind address %s: %w", s.cfg.SMTP.BindAddr, err)
	}
	s.listener = l
	s.mu.Unlock()

	return s.server.Serve(l)
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		_ = s.server.Close()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	return nil
}

func (s *Server) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{
		cfg:        s.cfg,
		mailSender: s.mailSender,
	}, nil
}

type Session struct {
	cfg          *config.Config
	mailSender   MailSender
	authenticated bool
	from         string
	to           []string
}

func (s *Session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *Session) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		if username == s.cfg.LocalAuth.Username && password == s.cfg.LocalAuth.Password {
			s.authenticated = true
			return nil
		}
		return smtp.ErrAuthFailed
	}), nil
}

func (s *Session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *Session) Logout() error {
	return nil
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	rawMIME, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read raw MIME body: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := s.mailSender.SendMail(ctx, rawMIME); err != nil {
		return &smtp.SMTPError{
			Code:         554,
			EnhancedCode: smtp.EnhancedCode{5, 0, 0},
			Message:      fmt.Sprintf("Failed to deliver mail to Microsoft Graph: %v", err),
		}
	}

	return nil
}
