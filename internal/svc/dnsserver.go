package svc

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"

	"aegis/internal/store"
)

// DNSServer is the panel's built-in authoritative name server. Zones are read
// live from the store, so record edits take effect immediately. It answers
// A, AAAA, CNAME, MX, TXT, NS, SRV and CAA records and returns SOA for
// negative responses.
type DNSServer struct {
	Store   *store.Store
	cfgNS   []string
	cfgSOA  string
	server  *dns.Server
	started bool
}

func NewDNSServer(st *store.Store, nameservers []string, adminEmail string) *DNSServer {
	return &DNSServer{Store: st, cfgNS: nameservers, cfgSOA: adminEmail}
}

// Start begins listening on addr (e.g. ":53"). Returns an error channel that
// receives fatal listen errors.
func (d *DNSServer) Start(addr string) (<-chan error, error) {
	if d.started {
		return nil, nil
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", d.handle)
	srv := &dns.Server{Addr: addr, Net: "udp", Handler: mux, ReadTimeout: 5 * time.Second}
	d.server = srv
	errCh := make(chan error, 1)
	go func() {
		slog.Info("dns: authoritative server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()
	d.started = true
	return errCh, nil
}

// Stop shuts the server down.
func (d *DNSServer) Stop() error {
	if d.server != nil {
		d.started = false
		return d.server.Shutdown()
	}
	return nil
}

// handle answers a single DNS query.
func (d *DNSServer) handle(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	qname := strings.ToLower(strings.TrimSuffix(r.Question[0].Name, "."))
	qtype := r.Question[0].Qtype

	zone, domain, relative := d.lookupZone(qname)
	if zone == nil {
		m.Rcode = dns.RcodeNameError
		_ = w.WriteMsg(m)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	records, err := d.Store.ListRecords(ctx, zone.ID)
	if err != nil {
		m.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(m)
		return
	}

	// Collect matching records: apex (@) or exact relative name.
	var matched []*store.DNSRecord
	for _, rec := range records {
		recName := strings.ToLower(rec.Name)
		if recName == "@" {
			recName = domain
		} else {
			recName = recName + "." + domain
		}
		if recName == relative || recName == qname {
			if qtype == dns.TypeANY || qtype == recordTypeToWire(rec.Type) {
				matched = append(matched, rec)
			}
		}
	}

	if len(matched) == 0 && qtype != dns.TypeSOA {
		m.Rcode = dns.RcodeNameError
	}

	// SOA for the zone (always include in authority on negative answers).
	soa := d.soaRecord(domain, time.Now().UTC().Format("2006010201"))
	m.Ns = append(m.Ns, soa)

	for _, rec := range matched {
		if rr := d.toRR(rec, domain); rr != nil {
			m.Answer = append(m.Answer, rr)
		}
	}
	// NS records at apex.
	if qtype == dns.TypeNS || qtype == dns.TypeANY {
		for _, ns := range d.cfgNS {
			m.Answer = append(m.Answer, &dns.NS{
				Hdr: dns.RR_Header{Name: dns.Fqdn(domain), Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 3600},
				Ns:  dns.Fqdn(ns),
			})
		}
	}
	_ = w.WriteMsg(m)
}

// lookupZone finds the zone whose domain is a suffix of qname.
func (d *DNSServer) lookupZone(qname string) (*store.DNSZone, string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	zones, err := d.Store.ListZones(ctx)
	if err != nil {
		return nil, "", ""
	}
	var best *store.DNSZone
	for _, z := range zones {
		zd := strings.ToLower(z.Domain)
		if qname == zd || strings.HasSuffix(qname, "."+zd) {
			if best == nil || len(z.Domain) > len(best.Domain) {
				best = z
			}
		}
	}
	if best == nil {
		return nil, "", ""
	}
	relative := strings.TrimSuffix(qname, "."+strings.ToLower(best.Domain))
	return best, best.Domain, relative
}

func (d *DNSServer) soaRecord(domain, serial string) *dns.SOA {
	ns := "ns1." + domain + "."
	admin := d.cfgSOA
	if admin == "" {
		admin = "hostmaster." + domain + "."
	} else if !strings.HasSuffix(admin, ".") {
		admin += "."
	}
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: dns.Fqdn(domain), Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      ns,
		Mbox:    admin,
		Serial:  mustSerial(serial),
		Refresh: 7200,
		Retry:   3600,
		Expire:  1209600,
		Minttl:  300,
	}
}

func mustSerial(s string) uint32 {
	var n uint32
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + uint32(c-'0')
		}
	}
	return n
}

func recordTypeToWire(t string) uint16 {
	switch t {
	case store.RecordA:
		return dns.TypeA
	case store.RecordAAAA:
		return dns.TypeAAAA
	case store.RecordCNAME:
		return dns.TypeCNAME
	case store.RecordMX:
		return dns.TypeMX
	case store.RecordTXT:
		return dns.TypeTXT
	case store.RecordNS:
		return dns.TypeNS
	case store.RecordSRV:
		return dns.TypeSRV
	case store.RecordCAA:
		return dns.TypeCAA
	}
	return dns.TypeNone
}

// toRR converts a store record into a miekg/dns RR.
func (d *DNSServer) toRR(rec *store.DNSRecord, domain string) dns.RR {
	name := rec.Name
	if name == "@" {
		name = domain
	}
	fqdn := dns.Fqdn(name)
	ttl := uint32(rec.TTL)
	if ttl == 0 {
		ttl = 3600
	}
	hdr := dns.RR_Header{Name: fqdn, Rrtype: recordTypeToWire(rec.Type), Class: dns.ClassINET, Ttl: ttl}
	switch rec.Type {
	case store.RecordA:
		return &dns.A{Hdr: hdr, A: net.ParseIP(rec.Content)}
	case store.RecordAAAA:
		return &dns.AAAA{Hdr: hdr, AAAA: net.ParseIP(rec.Content)}
	case store.RecordCNAME:
		return &dns.CNAME{Hdr: hdr, Target: dns.Fqdn(rec.Content)}
	case store.RecordMX:
		return &dns.MX{Hdr: hdr, Preference: uint16(rec.Priority), Mx: dns.Fqdn(rec.Content)}
	case store.RecordTXT:
		return &dns.TXT{Hdr: hdr, Txt: []string{rec.Content}}
	case store.RecordNS:
		return &dns.NS{Hdr: hdr, Ns: dns.Fqdn(rec.Content)}
	case store.RecordSRV:
		return &dns.SRV{Hdr: hdr, Priority: uint16(rec.Priority), Weight: 1, Port: uint16(rec.Priority), Target: dns.Fqdn(rec.Content)}
	case store.RecordCAA:
		return &dns.CAA{Hdr: hdr, Flag: 0, Tag: "issue", Value: rec.Content}
	}
	return nil
}
