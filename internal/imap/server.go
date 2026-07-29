package imap

import (
	"fmt"
	"net"
	"sync"

	"graph-mail-proxy/internal/config"
	"graph-mail-proxy/internal/graph"
	"graph-mail-proxy/internal/store"

	"github.com/emersion/go-imap/v2/imapserver"
)

type Server struct {
	cfg         *config.Config
	graphClient *graph.Client
	store       *store.Store
	server      *imapserver.Server
	listener    net.Listener
	mu          sync.Mutex
}

func NewServer(cfg *config.Config, graphClient *graph.Client, st *store.Store) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	s := &Server{
		cfg:         cfg,
		graphClient: graphClient,
		store:       st,
	}

	options := &imapserver.Options{
		InsecureAuth: true, // Allow plaintext over 127.0.0.1
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			sess := newSession(cfg, graphClient, st)
			return sess, nil, nil
		},
	}

	s.server = imapserver.New(options)
	return s, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	l, err := net.Listen("tcp", s.cfg.IMAP.BindAddr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on IMAP bind address %s: %w", s.cfg.IMAP.BindAddr, err)
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
