package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aegis/internal/store"
)

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// cloudflareProvider syncs zones to Cloudflare via the v4 API. The token must
// have Zone:Read, Zone:Edit, DNS:Read and DNS:Edit permissions for the zones
// being managed.
type cloudflareProvider struct {
	d     *DNS
	token string
	hc    *http.Client
}

// cfResponse is the shared Cloudflare API envelope.
type cfResponse struct {
	Success    bool            `json:"success"`
	Errors     []cfAPIError    `json:"errors"`
	Messages   []interface{}   `json:"messages"`
	Result     json.RawMessage `json:"result"`
	ResultInfo cfResultInfo    `json:"result_info"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

// cloudflareProvider looks up the configured provider row and builds the
// plugin. Requires a provider row with name "cloudflare".
func (d *DNS) cloudflareProvider() (Provider, error) {
	provs, err := d.Store.ListProviders(context.Background())
	if err != nil {
		return nil, err
	}
	for _, p := range provs {
		if p.Name == "cloudflare" && p.Enabled {
			token, err := d.Cipher.Decrypt(p.APIKeyEnc)
			if err != nil {
				return nil, fmt.Errorf("cloudflare: cannot decrypt token: %w", err)
			}
			return &cloudflareProvider{d: d, token: token, hc: &http.Client{Timeout: 30 * time.Second}}, nil
		}
	}
	return nil, errors.New("cloudflare: no enabled provider configured (add one in DNS → Providers)")
}

func (p *cloudflareProvider) Name() string { return "cloudflare" }

// do performs an API call and decodes the result into out (when non-nil).
func (p *cloudflareProvider) do(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env cfResponse
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("cloudflare: bad response: %s", truncate(string(data), 200))
	}
	if !env.Success {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		if len(msgs) == 0 {
			msgs = append(msgs, truncate(string(data), 300))
		}
		return fmt.Errorf("cloudflare: %s", strings.Join(msgs, "; "))
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// EnsureZone finds or creates the zone and returns its id.
func (p *cloudflareProvider) EnsureZone(ctx context.Context, domain string) (string, error) {
	var zones []cfZone
	if err := p.do(ctx, "GET", "/zones?name="+domain+"&per_page=50", nil, &zones); err == nil {
		for _, z := range zones {
			if strings.EqualFold(z.Name, domain) {
				return z.ID, nil
			}
		}
	}
	// Create the zone.
	var accounts []cfAccount
	if err := p.do(ctx, "GET", "/accounts?per_page=50", nil, &accounts); err != nil || len(accounts) == 0 {
		return "", fmt.Errorf("cloudflare: could not list accounts (token needs Account:Read): %w", err)
	}
	create := map[string]interface{}{
		"name":    domain,
		"account": map[string]string{"id": accounts[0].ID},
		"type":    "full",
	}
	var created cfZone
	if err := p.do(ctx, "POST", "/zones", create, &created); err != nil {
		return "", fmt.Errorf("cloudflare: zone creation failed: %w", err)
	}
	return created.ID, nil
}

// Push reconciles store records against the provider.
func (p *cloudflareProvider) Push(ctx context.Context, domain, providerZoneID string, records []*store.DNSRecord) error {
	existing, err := p.listRecords(ctx, providerZoneID)
	if err != nil {
		return err
	}
	desired := map[string]*store.DNSRecord{}
	for i := range records {
		key := recordKey(records[i])
		desired[key] = records[i]
	}
	// Delete provider records that no longer exist locally.
	for _, ex := range existing {
		key := cfRecordKey(ex)
		if _, ok := desired[key]; !ok {
			if err := p.do(ctx, "DELETE", "/zones/"+providerZoneID+"/dns_records/"+ex.ID, nil, nil); err != nil {
				return err
			}
		}
	}
	// Create missing records; update when content/ttl/proxy differ.
	for _, ex := range existing {
		key := cfRecordKey(ex)
		if rec, ok := desired[key]; ok {
			if rec.Content != ex.Content || rec.TTL != ex.TTL || !boolEqual(rec.Proxied, ex.Proxied) {
				_ = p.do(ctx, "PUT", "/zones/"+providerZoneID+"/dns_records/"+ex.ID, p.toCF(rec), nil)
			}
			delete(desired, key)
		}
	}
	for _, rec := range desired {
		if err := p.do(ctx, "POST", "/zones/"+providerZoneID+"/dns_records", p.toCF(rec), nil); err != nil {
			return fmt.Errorf("cloudflare: create %s %s: %w", rec.Type, rec.Name, err)
		}
	}
	return nil
}

func (p *cloudflareProvider) DeleteZone(ctx context.Context, domain, providerZoneID string) error {
	if providerZoneID == "" {
		return nil
	}
	// Deactivate then delete to avoid accidental purge; failures are surfaced.
	_ = p.do(ctx, "DELETE", "/zones/"+providerZoneID, nil, nil)
	return nil
}

func (p *cloudflareProvider) listRecords(ctx context.Context, zoneID string) ([]cfRecord, error) {
	var all []cfRecord
	page := 1
	for {
		var recs []cfRecord
		if err := p.do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?per_page=100&page=%d", zoneID, page), nil, &recs); err != nil {
			return nil, err
		}
		all = append(all, recs...)
		if len(recs) < 100 {
			break
		}
		page++
	}
	return all, nil
}

func (p *cloudflareProvider) toCF(r *store.DNSRecord) *cfRecord {
	rec := &cfRecord{Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL}
	if r.Type == store.RecordA || r.Type == store.RecordAAAA || r.Type == store.RecordCNAME {
		proxied := r.Proxied
		rec.Proxied = &proxied
	}
	if r.Type == store.RecordMX || r.Type == store.RecordSRV {
		prio := r.Priority
		rec.Priority = &prio
	}
	return rec
}

func recordKey(r *store.DNSRecord) string {
	return strings.ToLower(r.Type) + "|" + strings.ToLower(r.Name)
}

func cfRecordKey(r cfRecord) string {
	return strings.ToLower(r.Type) + "|" + strings.ToLower(r.Name)
}

func boolEqual(a bool, b *bool) bool {
	if b == nil {
		return !a
	}
	return a == *b
}

func nilBool(b bool) *bool { return &b }
