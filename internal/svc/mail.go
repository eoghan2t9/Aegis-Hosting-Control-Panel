package svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/crypto/bcrypt"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Mail provisions virtual mailboxes and aliases, publishes SPF/DKIM/DMARC
// into the DNS zone editor, and acts as the backend for the built-in webmail
// client. Postfix and Dovecot query the Aegis SQLite store directly (their
// own read-only sqlite maps against mail_domains/mailboxes/mail_aliases) —
// the panel never syncs credentials to a second store.
type Mail struct {
	Cfg   *config.Config
	Store *store.Store
	DNS   *DNS
}

func NewMail(cfg *config.Config, st *store.Store, dns *DNS) *Mail {
	return &Mail{Cfg: cfg, Store: st, DNS: dns}
}

const (
	dkimKeyDir  = "/etc/opendkim/keys"
	dkimKeyTbl  = "/etc/opendkim/KeyTable"
	dkimSignTbl = "/etc/opendkim/SigningTable"
	imapAddr    = "127.0.0.1:143"
	smtpAddr    = "127.0.0.1:25"
)

var localpartRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

// ValidLocalpart checks a mailbox's local part (before the @).
func ValidLocalpart(s string) bool { return localpartRe.MatchString(s) }

// --- domain enablement (DKIM + SPF/DKIM/DMARC DNS records) --------------------

// EnableDomain turns on mail for dom: generates a DKIM keypair, publishes
// SPF/DKIM/DMARC as TXT records into the domain's existing DNS zone (which
// must already exist — mail piggybacks on DNS management, it doesn't create
// zones itself), and registers the domain with OpenDKIM.
func (m *Mail) EnableDomain(ctx context.Context, dom *store.Domain) (*store.MailDomain, error) {
	if _, err := m.Store.GetMailDomainByDomainID(ctx, dom.ID); err == nil {
		return nil, errors.New("mail is already enabled for this domain")
	}
	zone, err := m.Store.GetZoneByDomain(ctx, dom.ID)
	if err != nil {
		return nil, errors.New("create a DNS zone for this domain first (DNS tab)")
	}

	pub, err := m.generateDKIMKey(dom.Domain)
	if err != nil {
		return nil, fmt.Errorf("generate DKIM key: %w", err)
	}
	dkimTXT := "v=DKIM1; h=sha256; k=rsa; p=" + pub

	md := &store.MailDomain{DomainID: dom.ID, Domain: dom.Domain, DKIMSelector: "default", DKIMPublicKey: dkimTXT}
	if err := m.Store.CreateMailDomain(ctx, md); err != nil {
		return nil, err
	}

	if err := m.regenerateDKIMTables(ctx); err != nil {
		return nil, fmt.Errorf("update opendkim tables: %w", err)
	}

	_ = m.upsertRecord(ctx, zone.ID, "@", store.RecordTXT, "v=spf1 mx ~all", 3600)
	_ = m.upsertRecord(ctx, zone.ID, "default._domainkey", store.RecordTXT, dkimTXT, 3600)
	_ = m.upsertRecord(ctx, zone.ID, "_dmarc", store.RecordTXT, "v=DMARC1; p=quarantine; rua=mailto:postmaster@"+dom.Domain, 3600)
	if m.DNS != nil {
		_, _ = m.DNS.Sync(ctx, zone.ID)
	}

	return md, nil
}

// DisableDomain removes mail for a domain: mailboxes/aliases cascade, the
// DKIM key/table entries are removed. DNS TXT records are left in place
// (the operator may want to keep SPF, or remove DNS separately) — only the
// DKIM key material and the mail_domains row are cleaned up here.
func (m *Mail) DisableDomain(ctx context.Context, md *store.MailDomain) error {
	boxes, err := m.Store.ListMailboxes(ctx, md.ID)
	if err != nil {
		return err
	}
	for _, b := range boxes {
		if err := m.Store.DeleteMailbox(ctx, b.ID); err != nil {
			return err
		}
	}
	aliases, err := m.Store.ListMailAliases(ctx, md.ID)
	if err != nil {
		return err
	}
	for _, a := range aliases {
		if err := m.Store.DeleteMailAlias(ctx, a.ID); err != nil {
			return err
		}
	}
	if err := m.Store.DeleteMailDomain(ctx, md.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(dkimKeyDir, md.Domain))
	return m.regenerateDKIMTables(ctx)
}

// generateDKIMKey creates a 2048-bit RSA keypair, writes the private key
// under dkimKeyDir/<domain>/default.private (owned by the opendkim service
// user), and returns the base64 SubjectPublicKeyInfo for the DNS TXT record.
func (m *Mail) generateDKIMKey(domain string) (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dkimKeyDir, domain)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(filepath.Join(dir, "default.private"), priv, 0o640); err != nil {
		return "", err
	}
	_, _ = RunTimeout(10*time.Second, "chown", "-R", "opendkim:opendkim", dir)

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pubDER), nil
}

// regenerateDKIMTables rewrites OpenDKIM's KeyTable/SigningTable from every
// mail-enabled domain's row (the "regenerate config from the DB" idiom used
// throughout the panel) and reloads the service.
func (m *Mail) regenerateDKIMTables(ctx context.Context) error {
	domains, err := m.Store.ListMailDomains(ctx)
	if err != nil {
		return err
	}
	var keyTbl, signTbl strings.Builder
	for _, d := range domains {
		sel := d.DKIMSelector
		fmt.Fprintf(&keyTbl, "%s._domainkey.%s %s:%s:%s\n", sel, d.Domain, d.Domain, sel, filepath.Join(dkimKeyDir, d.Domain, "default.private"))
		fmt.Fprintf(&signTbl, "*@%s %s._domainkey.%s\n", d.Domain, sel, d.Domain)
	}
	if err := os.WriteFile(dkimKeyTbl, []byte(keyTbl.String()), 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(dkimSignTbl, []byte(signTbl.String()), 0o640); err != nil {
		return err
	}
	_, _ = RunTimeout(10*time.Second, "chown", "opendkim:opendkim", dkimKeyTbl, dkimSignTbl)
	restartService("opendkim")
	return nil
}

// restartService (re)starts a mail-stack daemon: supervisord inside the dev
// container, systemd on bare metal. Both attempts are best-effort — the
// caller reports success even if the daemon needs a manual restart.
func restartService(name string) {
	if _, err := RunTimeout(10*time.Second, "supervisorctl", "restart", name); err == nil {
		return
	}
	if systemdIsInit() {
		if _, err := RunTimeout(15*time.Second, "systemctl", "restart", name); err == nil {
			return
		}
	}
}

// upsertRecord creates or updates a DNS record by (name, type) within a zone.
func (m *Mail) upsertRecord(ctx context.Context, zoneID int64, name, recType, content string, ttl int) error {
	existing, err := m.Store.ListRecords(ctx, zoneID)
	if err == nil {
		for _, r := range existing {
			if strings.EqualFold(r.Name, name) && r.Type == recType {
				r.Content = content
				r.TTL = ttl
				return m.Store.UpdateRecord(ctx, r)
			}
		}
	}
	rec := &store.DNSRecord{ZoneID: zoneID, Name: name, Type: recType, TTL: ttl, Content: content}
	if err := ValidateRecord(rec); err != nil {
		return err
	}
	return m.Store.CreateRecord(ctx, rec)
}

// --- mailboxes ----------------------------------------------------------------

// bcryptDovecot hashes password for Dovecot's passdb sql, which expects the
// scheme-prefixed form so it doesn't need a default_pass_scheme guess.
func bcryptDovecot(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return "{BLF-CRYPT}" + string(h), nil
}

func (m *Mail) CreateMailbox(ctx context.Context, md *store.MailDomain, localpart, password string, quotaBytes int64) (*store.Mailbox, error) {
	localpart = strings.ToLower(strings.TrimSpace(localpart))
	if !ValidLocalpart(localpart) {
		return nil, errors.New("invalid mailbox name (lowercase letters, digits, dot, underscore, hyphen)")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if _, err := m.Store.GetMailboxByAddress(ctx, md.Domain, localpart); err == nil {
		return nil, fmt.Errorf("mailbox %s@%s already exists", localpart, md.Domain)
	}
	hash, err := bcryptDovecot(password)
	if err != nil {
		return nil, err
	}
	home := filepath.Join("/var/mail/vhosts", md.Domain, localpart)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	_, _ = RunTimeout(10*time.Second, "chown", "-R", "5000:5000", filepath.Join("/var/mail/vhosts", md.Domain))

	box := &store.Mailbox{MailDomainID: md.ID, Localpart: localpart, PasswordHash: hash, QuotaBytes: quotaBytes, Enabled: true}
	if err := m.Store.CreateMailbox(ctx, box); err != nil {
		return nil, err
	}
	box.Domain = md.Domain
	return box, nil
}

func (m *Mail) ResetMailboxPassword(ctx context.Context, box *store.Mailbox, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcryptDovecot(password)
	if err != nil {
		return err
	}
	box.PasswordHash = hash
	return m.Store.UpdateMailbox(ctx, box)
}

func (m *Mail) ToggleMailbox(ctx context.Context, box *store.Mailbox, enabled bool) error {
	box.Enabled = enabled
	return m.Store.UpdateMailbox(ctx, box)
}

func (m *Mail) DeleteMailbox(ctx context.Context, box *store.Mailbox) error {
	if err := m.Store.DeleteMailbox(ctx, box.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join("/var/mail/vhosts", box.Domain, box.Localpart))
	return nil
}

// --- aliases --------------------------------------------------------------

func (m *Mail) CreateAlias(ctx context.Context, md *store.MailDomain, source, destination string) (*store.MailAlias, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	destination = strings.ToLower(strings.TrimSpace(destination))
	if !ValidLocalpart(source) {
		return nil, errors.New("invalid alias source")
	}
	if destination == "" || !strings.Contains(destination, "@") {
		return nil, errors.New("destination must be a full email address")
	}
	a := &store.MailAlias{MailDomainID: md.ID, Source: source, Destination: destination}
	if err := m.Store.CreateMailAlias(ctx, a); err != nil {
		return nil, err
	}
	a.Domain = md.Domain
	return a, nil
}

func (m *Mail) DeleteAlias(ctx context.Context, a *store.MailAlias) error {
	return m.Store.DeleteMailAlias(ctx, a.ID)
}

// --- webmail: IMAP (read) + SMTP (send) ----------------------------------------

// MessageSummary is one row in the webmail message list.
type MessageSummary struct {
	UID     uint32    `json:"uid"`
	Subject string    `json:"subject"`
	From    string    `json:"from"`
	Date    time.Time `json:"date"`
	Seen    bool      `json:"seen"`
}

// MessageDetail is a single fetched, parsed message.
type MessageDetail struct {
	UID      uint32    `json:"uid"`
	Subject  string    `json:"subject"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Date     time.Time `json:"date"`
	TextBody string    `json:"text_body"`
	HTMLBody string    `json:"html_body,omitempty"` // sanitized before being set
}

// imapLogin dials Dovecot and authenticates as address (the mailbox's own
// login, "localpart@domain") — this is a separate credential from the panel
// JWT; webmail sessions hold it only long enough to serve one request.
func imapLogin(address, password string) (*imapclient.Client, error) {
	c, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to mail server: %w", err)
	}
	if err := c.Login(address, password).Wait(); err != nil {
		c.Close()
		return nil, errors.New("invalid mailbox credentials")
	}
	return c, nil
}

// ListMessages returns the INBOX contents, newest first.
func (m *Mail) ListMessages(address, password string) ([]MessageSummary, error) {
	c, err := imapLogin(address, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Logout().Wait(); c.Close() }()

	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("select inbox: %w", err)
	}
	out := []MessageSummary{}
	if sel.NumMessages == 0 {
		return out, nil
	}
	nums := make([]uint32, sel.NumMessages)
	for i := range nums {
		nums[i] = uint32(i + 1)
	}
	msgs, err := c.Fetch(imap.SeqSetNum(nums...), &imap.FetchOptions{Envelope: true, UID: true, Flags: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch messages: %w", err)
	}
	for _, msg := range msgs {
		s := MessageSummary{UID: uint32(msg.UID)}
		if msg.Envelope != nil {
			s.Subject = msg.Envelope.Subject
			s.Date = msg.Envelope.Date
			if len(msg.Envelope.From) > 0 {
				s.From = msg.Envelope.From[0].Addr()
			}
		}
		for _, f := range msg.Flags {
			if f == imap.FlagSeen {
				s.Seen = true
			}
		}
		out = append(out, s)
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

var htmlSanitizer = bluemonday.UGCPolicy()

// GetMessage fetches and parses one message by UID, sanitizing any HTML body
// server-side before it ever reaches the frontend.
func (m *Mail) GetMessage(address, password string, uid uint32) (*MessageDetail, error) {
	c, err := imapLogin(address, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Logout().Wait(); c.Close() }()

	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select inbox: %w", err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}).Collect()
	if err != nil || len(msgs) == 0 {
		return nil, errors.New("message not found")
	}
	msg := msgs[0]
	detail := &MessageDetail{UID: uid}
	if msg.Envelope != nil {
		detail.Subject = msg.Envelope.Subject
		detail.Date = msg.Envelope.Date
		if len(msg.Envelope.From) > 0 {
			detail.From = msg.Envelope.From[0].Addr()
		}
		if len(msg.Envelope.To) > 0 {
			detail.To = msg.Envelope.To[0].Addr()
		}
	}
	if len(msg.BodySection) > 0 {
		text, html := parseMIMEBody(msg.BodySection[0].Bytes)
		detail.TextBody = text
		if html != "" {
			detail.HTMLBody = htmlSanitizer.Sanitize(html)
		}
	}
	return detail, nil
}

// parseMIMEBody extracts the plain-text and HTML parts of a raw RFC822
// message using the standard library only (no third MIME-parsing dependency
// beyond what go-imap already requires).
func parseMIMEBody(raw []byte) (text, html string) {
	msg, err := mail.ReadMessage(newByteReader(raw))
	if err != nil {
		return string(raw), ""
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		body, _ := io.ReadAll(decodeTransfer(msg.Header.Get("Content-Transfer-Encoding"), msg.Body))
		if strings.HasPrefix(mediaType, "text/html") {
			return "", string(body)
		}
		return string(body), ""
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		ct := part.Header.Get("Content-Type")
		body, _ := io.ReadAll(decodeTransfer(part.Header.Get("Content-Transfer-Encoding"), part))
		switch {
		case strings.HasPrefix(ct, "text/plain") && text == "":
			text = string(body)
		case strings.HasPrefix(ct, "text/html") && html == "":
			html = string(body)
		case strings.HasPrefix(ct, "multipart/"):
			if _, params2, err := mime.ParseMediaType(ct); err == nil {
				t, h := parseMultipart(part, params2["boundary"])
				if text == "" {
					text = t
				}
				if html == "" {
					html = h
				}
			}
		}
	}
	return text, html
}

func parseMultipart(r io.Reader, boundary string) (text, html string) {
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		ct := part.Header.Get("Content-Type")
		body, _ := io.ReadAll(decodeTransfer(part.Header.Get("Content-Transfer-Encoding"), part))
		if strings.HasPrefix(ct, "text/plain") && text == "" {
			text = string(body)
		}
		if strings.HasPrefix(ct, "text/html") && html == "" {
			html = string(body)
		}
	}
	return text, html
}

func decodeTransfer(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// newByteReader avoids pulling in "bytes" solely for a one-shot io.Reader
// over an already-in-memory message.
func newByteReader(b []byte) io.Reader { return &byteReader{b: b} }

type byteReader struct {
	b   []byte
	off int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

// SendMessage submits a plain-text message via the local Postfix (trusted
// loopback client, no SASL needed — mynetworks covers 127.0.0.0/8). The
// From address is always the authenticated mailbox's own address; the
// caller (API layer) is responsible for having verified address/password
// via ListMessages/GetMessage or an equivalent IMAP login first.
func (m *Mail) SendMessage(fromAddress, to, subject, body string) error {
	if to == "" || !strings.Contains(to, "@") {
		return errors.New("a valid recipient address is required")
	}
	header := textproto.MIMEHeader{}
	header.Set("From", fromAddress)
	header.Set("To", to)
	header.Set("Subject", subject)
	header.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	header.Set("Content-Type", "text/plain; charset=utf-8")

	var sb strings.Builder
	for k, vs := range header {
		for _, v := range vs {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)

	return smtp.SendMail(smtpAddr, nil, fromAddress, []string{to}, []byte(sb.String()))
}
