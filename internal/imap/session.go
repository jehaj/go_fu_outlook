package imap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"graph-mail-proxy/internal/config"
	"graph-mail-proxy/internal/graph"
	"graph-mail-proxy/internal/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
)

type Session struct {
	cfg          *config.Config
	graphClient   *graph.Client
	store        *store.Store
	authenticated bool
	selectedFolder *store.FolderState
	mu           sync.Mutex
}

func newSession(cfg *config.Config, graphClient *graph.Client, st *store.Store) *Session {
	return &Session{
		cfg:         cfg,
		graphClient: graphClient,
		store:       st,
	}
}

func (s *Session) Close() error {
	return nil
}

func (s *Session) Login(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if username == s.cfg.LocalAuth.Username && password == s.cfg.LocalAuth.Password {
		s.authenticated = true
		return nil
	}
	return imapserver.ErrAuthFailed
}

func (s *Session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	s.mu.Lock()
	authed := s.authenticated
	s.mu.Unlock()

	if !authed {
		return imapserver.ErrAuthFailed
	}

	ctx := context.Background()
	folders, err := s.graphClient.ListFolders(ctx)
	if err != nil {
		// Fallback to local default folders if network error
		defaultFolders := []string{"INBOX", "Sent Items", "Drafts", "Deleted Items"}
		for _, name := range defaultFolders {
			_ = w.WriteList(&imap.ListData{
				Mailbox: name,
				Delim:   '/',
			})
		}
		return nil
	}

	for _, folder := range folders {
		_, _ = s.store.EnsureFolder(folder.ID, folder.DisplayName)
		_ = w.WriteList(&imap.ListData{
			Mailbox: folder.DisplayName,
			Delim:   '/',
		})
	}
	return nil
}

func (s *Session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.authenticated {
		return nil, imapserver.ErrAuthFailed
	}

	folderID := resolveFolderID(mailbox)
	state, err := s.store.EnsureFolder(folderID, mailbox)
	if err != nil {
		return nil, err
	}

	// Fetch latest messages from Graph
	ctx := context.Background()
	graphMsgs, err := s.graphClient.ListMessages(ctx, folderID, 100, 0)
	if err == nil {
		for _, m := range graphMsgs {
			size := int64(len(m.BodyPreview)) + 500
			_, _ = s.store.MapMessage(folderID, m.ID, m.Subject, m.IsRead, m.IsDraft, m.CreatedDateTime, size)
		}
	}

	msgs, err := s.store.ListMessages(folderID)
	if err != nil {
		return nil, err
	}

	s.selectedFolder = state

	return &imap.SelectData{
		Flags: []imap.Flag{
			imap.FlagSeen,
			imap.FlagDraft,
			imap.FlagDeleted,
		},
		PermanentFlags: []imap.Flag{
			imap.FlagSeen,
			imap.FlagDraft,
			imap.FlagDeleted,
		},
		NumMessages: uint32(len(msgs)),
		UIDValidity: state.UIDValidity,
		UIDNext:     imap.UID(state.NextUID),
	}, nil
}

func (s *Session) Unselect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedFolder = nil
	return nil
}

func (s *Session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	folderID := resolveFolderID(mailbox)
	state, err := s.store.EnsureFolder(folderID, mailbox)
	if err != nil {
		return nil, err
	}

	msgs, _ := s.store.ListMessages(folderID)
	numMsgs := uint32(len(msgs))
	res := &imap.StatusData{
		Mailbox:     mailbox,
		NumMessages: &numMsgs,
		UIDValidity: state.UIDValidity,
		UIDNext:     imap.UID(state.NextUID),
	}

	unreadCount := uint32(0)
	for _, m := range msgs {
		if !m.IsRead {
			unreadCount++
		}
	}
	res.NumUnseen = &unreadCount

	return res, nil
}

func (s *Session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	s.mu.Lock()
	selected := s.selectedFolder
	s.mu.Unlock()

	if selected == nil {
		return fmt.Errorf("no mailbox selected")
	}

	msgs, err := s.store.ListMessages(selected.ID)
	if err != nil {
		return err
	}

	ctx := context.Background()

	for i, msg := range msgs {
		seqNum := uint32(i + 1)
		uid := imap.UID(msg.UID)

		if !matchesNumSet(seqNum, uid, numSet) {
			continue
		}

		itemWriter := w.CreateMessage(seqNum)

		if options.UID {
			itemWriter.WriteUID(uid)
		}

		var flags []imap.Flag
		if msg.IsRead {
			flags = append(flags, imap.FlagSeen)
		}
		if msg.IsDraft {
			flags = append(flags, imap.FlagDraft)
		}
		if msg.IsDeleted {
			flags = append(flags, imap.FlagDeleted)
		}
		itemWriter.WriteFlags(flags)

		if options.RFC822Size {
			itemWriter.WriteRFC822Size(msg.Size)
		}

		if options.InternalDate {
			itemWriter.WriteInternalDate(msg.ReceivedAt)
		}

		// Download full MIME if body or envelope requested
		var mimeBytes []byte
		if options.Envelope || len(options.BodySection) > 0 {
			mimeBytes, _ = s.graphClient.FetchMIME(ctx, msg.GraphID)
			if len(mimeBytes) == 0 {
				// Fallback MIME header & body if Graph fetch fails or empty
				mimeBytes = []byte(fmt.Sprintf("From: sender@example.com\r\nTo: me@example.com\r\nSubject: %s\r\nDate: %s\r\n\r\n(Body unavailable)", msg.Subject, msg.ReceivedAt.Format(time.RFC1123Z)))
			}
		}

		if options.Envelope {
			header := parseHeaders(mimeBytes)
			itemWriter.WriteEnvelope(imapserver.ExtractEnvelope(header))
		}

		for _, bs := range options.BodySection {
			wc := itemWriter.WriteBodySection(bs, int64(len(mimeBytes)))
			if wc != nil {
				_, _ = wc.Write(mimeBytes)
				_ = wc.Close()
			}
		}

		_ = itemWriter.Close()
	}

	return nil
}

func (s *Session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	s.mu.Lock()
	selected := s.selectedFolder
	s.mu.Unlock()

	if selected == nil {
		return fmt.Errorf("no mailbox selected")
	}

	msgs, err := s.store.ListMessages(selected.ID)
	if err != nil {
		return err
	}

	ctx := context.Background()

	for i, msg := range msgs {
		seqNum := uint32(i + 1)
		uid := imap.UID(msg.UID)

		if !matchesNumSet(seqNum, uid, numSet) {
			continue
		}

		for _, flag := range flags.Flags {
			if flag == imap.FlagSeen {
				isRead := (flags.Op == imap.StoreFlagsSet || flags.Op == imap.StoreFlagsAdd)
				_ = s.store.UpdateMessageFlags(selected.ID, msg.UID, isRead)
				_ = s.graphClient.UpdateMessageFlags(ctx, msg.GraphID, &isRead, nil)
			}
			if flag == imap.FlagDeleted {
				if flags.Op == imap.StoreFlagsSet || flags.Op == imap.StoreFlagsAdd {
					_ = s.store.MarkDeleted(selected.ID, msg.UID)
				}
			}
		}
	}

	return nil
}

func (s *Session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.mu.Lock()
	selected := s.selectedFolder
	s.mu.Unlock()

	if selected == nil {
		return fmt.Errorf("no mailbox selected")
	}

	ctx := context.Background()
	msgs, _ := s.store.ListMessages(selected.ID)

	expunged, err := s.store.ExpungeDeleted(selected.ID)
	if err != nil {
		return err
	}

	for _, uidVal := range expunged {
		for _, m := range msgs {
			if m.UID == uidVal {
				_ = s.graphClient.DeleteMessage(ctx, m.GraphID)
			}
		}
		if w != nil {
			_ = w.WriteExpunge(uidVal)
		}
	}

	return nil
}

func (s *Session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	s.mu.Lock()
	selected := s.selectedFolder
	s.mu.Unlock()

	if selected == nil {
		return nil, fmt.Errorf("no mailbox selected")
	}

	msgs, err := s.store.ListMessages(selected.ID)
	if err != nil {
		return nil, err
	}

	res := &imap.SearchData{}
	if kind == imapserver.NumKindUID {
		var uids imap.UIDSet
		for _, m := range msgs {
			uids.AddNum(imap.UID(m.UID))
		}
		res.All = uids
	} else {
		var seqs imap.SeqSet
		for i := range msgs {
			seqs.AddNum(uint32(i + 1))
		}
		res.All = seqs
	}

	return res, nil
}

func (s *Session) Create(mailbox string, options *imap.CreateOptions) error { return nil }
func (s *Session) Delete(mailbox string) error                        { return nil }
func (s *Session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	return nil
}
func (s *Session) Subscribe(mailbox string) error   { return nil }
func (s *Session) Unsubscribe(mailbox string) error { return nil }
func (s *Session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	return &imap.AppendData{}, nil
}
func (s *Session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error { return nil }
func (s *Session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}
func (s *Session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	return &imap.CopyData{}, nil
}

func resolveFolderID(name string) string {
	lower := strings.ToLower(name)
	switch lower {
	case "inbox":
		return "inbox"
	case "sent items", "sent":
		return "sentitems"
	case "drafts":
		return "drafts"
	case "deleted items", "trash":
		return "deleteditems"
	default:
		return name
	}
}

func matchesNumSet(seqNum uint32, uid imap.UID, numSet imap.NumSet) bool {
	switch ns := numSet.(type) {
	case imap.SeqSet:
		return ns.Contains(seqNum)
	case imap.UIDSet:
		return ns.Contains(uid)
	default:
		return true
	}
}

func uint32Ptr(val uint32) *uint32 {
	return &val
}

func parseHeaders(mimeBytes []byte) textproto.Header {
	var header textproto.Header
	lines := strings.Split(string(mimeBytes), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			header.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
	return header
}
