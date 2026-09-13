package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// --- ownership -----------------------------------------------------------------

// ownsMailDomain reports whether the caller may manage md (via the domain it
// mail-enables), mirroring how DNS zones are checked against their domain.
func (s *Server) ownsMailDomain(r *http.Request, md *store.MailDomain) bool {
	dom, err := s.Store.GetDomain(r.Context(), md.DomainID)
	if err != nil {
		return false
	}
	return s.ownsDomain(r, dom)
}

func (s *Server) mailDomainFromPath(r *http.Request) (*store.MailDomain, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, false
	}
	md, err := s.Store.GetMailDomain(r.Context(), id)
	if err != nil {
		return nil, false
	}
	if !s.ownsMailDomain(r, md) {
		return nil, false
	}
	return md, true
}

func (s *Server) mailboxFromPath(r *http.Request) (*store.Mailbox, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, false
	}
	box, err := s.Store.GetMailbox(r.Context(), id)
	if err != nil {
		return nil, false
	}
	// Resolve ownership via the mailbox's own mail domain row.
	mds, err := s.Store.ListMailDomains(r.Context())
	if err != nil {
		return nil, false
	}
	for _, d := range mds {
		if d.ID == box.MailDomainID {
			if !s.ownsMailDomain(r, d) {
				return nil, false
			}
			return box, true
		}
	}
	return nil, false
}

// --- mail domains ----------------------------------------------------------------

func (s *Server) handleMailDomainsList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	all, err := s.Store.ListMailDomains(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []*store.MailDomain{}
	for _, md := range all {
		if u.Role == store.RoleAdmin || s.ownsMailDomain(r, md) {
			out = append(out, md)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type mailDomainCreateReq struct {
	DomainID int64 `json:"domain_id"`
}

func (s *Server) handleMailDomainsCreate(w http.ResponseWriter, r *http.Request) {
	var req mailDomainCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	dom, err := s.Store.GetDomain(r.Context(), req.DomainID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	md, err := s.Mail.EnableDomain(r.Context(), dom)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "mail.domain-enable", dom.Domain, "")
	writeJSON(w, http.StatusCreated, md)
}

func (s *Server) handleMailDomainsDelete(w http.ResponseWriter, r *http.Request) {
	md, ok := s.mailDomainFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mail domain not found")
		return
	}
	if err := s.Mail.DisableDomain(r.Context(), md); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "mail.domain-disable", md.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- mailboxes ----------------------------------------------------------------

func (s *Server) handleMailboxesList(w http.ResponseWriter, r *http.Request) {
	md, ok := s.mailDomainFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mail domain not found")
		return
	}
	boxes, err := s.Store.ListMailboxes(r.Context(), md.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, boxes)
}

type mailboxCreateReq struct {
	Localpart  string `json:"localpart"`
	Password   string `json:"password"`
	QuotaBytes int64  `json:"quota_bytes"`
}

func (s *Server) handleMailboxesCreate(w http.ResponseWriter, r *http.Request) {
	md, ok := s.mailDomainFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mail domain not found")
		return
	}
	var req mailboxCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	box, err := s.Mail.CreateMailbox(r.Context(), md, req.Localpart, req.Password, req.QuotaBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "mailbox.create", box.Localpart+"@"+box.Domain, "")
	writeJSON(w, http.StatusCreated, box)
}

func (s *Server) handleMailboxPassword(w http.ResponseWriter, r *http.Request) {
	box, ok := s.mailboxFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mailbox not found")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Mail.ResetMailboxPassword(r.Context(), box, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "mailbox.password", box.Localpart+"@"+box.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMailboxToggle(w http.ResponseWriter, r *http.Request) {
	box, ok := s.mailboxFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mailbox not found")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Mail.ToggleMailbox(r.Context(), box, req.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, box)
}

func (s *Server) handleMailboxDelete(w http.ResponseWriter, r *http.Request) {
	box, ok := s.mailboxFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mailbox not found")
		return
	}
	if err := s.Mail.DeleteMailbox(r.Context(), box); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "mailbox.delete", box.Localpart+"@"+box.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- aliases --------------------------------------------------------------

func (s *Server) handleMailAliasesList(w http.ResponseWriter, r *http.Request) {
	md, ok := s.mailDomainFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mail domain not found")
		return
	}
	aliases, err := s.Store.ListMailAliases(r.Context(), md.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aliases)
}

type mailAliasCreateReq struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (s *Server) handleMailAliasesCreate(w http.ResponseWriter, r *http.Request) {
	md, ok := s.mailDomainFromPath(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "mail domain not found")
		return
	}
	var req mailAliasCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	a, err := s.Mail.CreateAlias(r.Context(), md, req.Source, req.Destination)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "alias.create", a.Source+"@"+a.Domain, a.Destination)
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleMailAliasesDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.Store.GetMailAlias(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "alias not found")
		return
	}
	md, err := s.Store.GetMailDomain(r.Context(), a.MailDomainID)
	if err != nil || !s.ownsMailDomain(r, md) {
		writeErr(w, http.StatusNotFound, "alias not found")
		return
	}
	if err := s.Mail.DeleteAlias(r.Context(), a); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "alias.delete", a.Source+"@"+a.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- webmail: its own short-lived credential session, separate from the panel JWT --

type webmailSession struct {
	address  string
	password string
	expires  time.Time
}

var (
	webmailMu       sync.Mutex
	webmailSessions = map[string]webmailSession{}
)

const webmailSessionTTL = 30 * time.Minute

func webmailToken(r *http.Request) string {
	if tok := r.Header.Get("X-Webmail-Token"); tok != "" {
		return tok
	}
	return r.URL.Query().Get("webmail_token")
}

func webmailSessionFrom(r *http.Request) (webmailSession, bool) {
	webmailMu.Lock()
	defer webmailMu.Unlock()
	sess, ok := webmailSessions[webmailToken(r)]
	if !ok || time.Now().After(sess.expires) {
		return webmailSession{}, false
	}
	return sess, true
}

// withWebmailAuth requires a valid webmail session token (minted by
// handleWebmailLogin), independent of the panel's own Bearer JWT.
func (s *Server) withWebmailAuth(next func(http.ResponseWriter, *http.Request, webmailSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := webmailSessionFrom(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid or expired webmail session")
			return
		}
		next(w, r, sess)
	}
}

type webmailLoginReq struct {
	Address  string `json:"address"`
	Password string `json:"password"`
}

func (s *Server) handleWebmailLogin(w http.ResponseWriter, r *http.Request) {
	var req webmailLoginReq
	if !readJSON(w, r, &req) {
		return
	}
	// Package gate: webmail sessions authenticate with mailbox credentials,
	// not the panel JWT, so resolve the mailbox's mail domain back to its
	// owning panel user and check their package's allow_webmail flag.
	localpart, domain, ok := strings.Cut(req.Address, "@")
	if ok {
		if mb, err := s.Store.GetMailboxByAddress(r.Context(), domain, localpart); err == nil {
			if md, err := s.Store.GetMailDomain(r.Context(), mb.MailDomainID); err == nil {
				if dom, err := s.Store.GetDomain(r.Context(), md.DomainID); err == nil {
					if owner, err := s.Store.GetUserByID(r.Context(), dom.UserID); err == nil &&
						!s.packageAllows(owner, s.featuresFor(r.Context(), owner)[FeatureWebmail]) {
						writeErr(w, http.StatusForbidden, "webmail is not enabled for this account's package")
						return
					}
				}
			}
		}
	}
	// A cheap login+logout round trip both validates the credentials and
	// avoids ever persisting the password anywhere but this in-memory session.
	if _, err := s.Mail.ListMessages(req.Address, req.Password); err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid mailbox credentials")
		return
	}
	tok, err := svc.RandomString(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	webmailMu.Lock()
	webmailSessions[tok] = webmailSession{address: req.Address, password: req.Password, expires: time.Now().Add(webmailSessionTTL)}
	webmailMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "address": req.Address})
}

func (s *Server) handleWebmailLogout(w http.ResponseWriter, r *http.Request) {
	webmailMu.Lock()
	delete(webmailSessions, webmailToken(r))
	webmailMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWebmailMessages(w http.ResponseWriter, r *http.Request, sess webmailSession) {
	msgs, err := s.Mail.ListMessages(sess.address, sess.password)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleWebmailMessageGet(w http.ResponseWriter, r *http.Request, sess webmailSession) {
	id, err := pathID(r, "uid")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid message id")
		return
	}
	msg, err := s.Mail.GetMessage(sess.address, sess.password, uint32(id))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

type webmailSendReq struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (s *Server) handleWebmailSend(w http.ResponseWriter, r *http.Request, sess webmailSession) {
	var req webmailSendReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Mail.SendMessage(sess.address, req.To, req.Subject, req.Body); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "webmail.send", sess.address, "to="+req.To)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
