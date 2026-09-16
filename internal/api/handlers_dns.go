package api

import (
	"net/http"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// handleDNSPopulate backfills a default DNS zone (see
// svc.Domains.PopulateDefaultDNS) for every domain missing one — admins
// cover every domain, everyone else only their own. Used by the DNS page's
// "Populate DNS" button to catch up domains created before DNS
// auto-provisioning existed.
func (s *Server) handleDNSPopulate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ownerID := u.ID
	if u.Role == store.RoleAdmin {
		ownerID = 0
	}
	created, skipped, failed := s.Domains.PopulateDefaultDNS(r.Context(), ownerID)
	s.audit(r, "dns.populate", "", "")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"created": created, "skipped": skipped, "failed": failed,
	})
}

// canAccessZone: users can access zones of their own domains; admins all.
func (s *Server) canAccessZone(r *http.Request, zone *store.DNSZone) bool {
	dom, err := s.Store.GetDomain(r.Context(), zone.DomainID)
	if err != nil {
		return false
	}
	return s.ownsDomain(r, dom)
}

// --- zones -------------------------------------------------------------------

func (s *Server) handleZonesList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	zones, err := s.Store.ListZones(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []*store.DNSZone{}
	for _, z := range zones {
		if u.Role == store.RoleAdmin || s.canAccessZone(r, z) {
			out = append(out, z)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleZonesGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone, err := s.Store.GetZone(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	records, err := s.Store.ListRecords(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"zone": zone, "records": records})
}

type zoneReq struct {
	DomainID int64  `json:"domain_id"`
	Provider string `json:"provider"`
}

func (s *Server) handleZonesCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req zoneReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.DomainID == 0 {
		writeErr(w, http.StatusBadRequest, "domain_id is required")
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
	if !s.packageAllows(u, s.featuresFor(r.Context(), u)[FeatureDNS]) {
		writeErr(w, http.StatusForbidden, "your package does not allow DNS management")
		return
	}
	if _, err := s.Store.GetZoneByDomain(r.Context(), req.DomainID); err == nil {
		writeErr(w, http.StatusConflict, "zone already exists for this domain")
		return
	}
	provider := req.Provider
	if provider == "" {
		provider = "local"
	}
	zone := &store.DNSZone{DomainID: req.DomainID, Provider: provider}
	if err := s.Store.CreateZone(r.Context(), zone); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Seed a default A record pointing at the server's primary IP.
	if ip := s.primaryIP(); ip != "" {
		rec := &store.DNSRecord{ZoneID: zone.ID, Name: "@", Type: store.RecordA, TTL: 3600, Content: ip}
		_ = s.Store.CreateRecord(r.Context(), rec)
	}
	s.audit(r, "dns.zone-create", dom.Domain, "provider="+provider)
	writeJSON(w, http.StatusCreated, zone)
}

func (s *Server) handleZonesUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone, err := s.Store.GetZone(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Provider != "" {
		zone.Provider = req.Provider
	}
	if err := s.Store.UpdateZone(r.Context(), zone); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, zone)
}

func (s *Server) handleZonesDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone, err := s.Store.GetZone(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	if err := s.DNS.DeleteZone(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	// Remove the zone row + records.
	records, _ := s.Store.ListRecords(r.Context(), id)
	for _, rec := range records {
		_ = s.Store.DeleteRecord(r.Context(), rec.ID)
	}
	// zones table has no cascade from domain; delete the zone row directly.
	if err := s.deleteZoneRow(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "dns.zone-delete", zone.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleZonesSync(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone, err := s.Store.GetZone(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	result, err := s.DNS.Sync(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, result.Error)
		return
	}
	s.audit(r, "dns.sync", zone.Domain, "provider="+zone.Provider)
	writeJSON(w, http.StatusOK, result)
}

// deleteZoneRow removes the zone row directly (SQLite).
func (s *Server) deleteZoneRow(id int64) error {
	_, err := s.Store.DB().Exec("DELETE FROM dns_zones WHERE id = ?", id)
	return err
}

// --- records --------------------------------------------------------------------

type recordReq struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Content  string `json:"content"`
	Proxied  bool   `json:"proxied"`
}

func (s *Server) handleRecordsCreate(w http.ResponseWriter, r *http.Request) {
	zoneID, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone, err := s.Store.GetZone(r.Context(), zoneID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	var req recordReq
	if !readJSON(w, r, &req) {
		return
	}
	rec := &store.DNSRecord{
		ZoneID: zoneID, Name: req.Name, Type: req.Type, TTL: req.TTL,
		Priority: req.Priority, Content: req.Content, Proxied: req.Proxied,
	}
	if err := svc.ValidateRecord(rec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateRecord(r.Context(), rec); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "dns.record-create", zone.Domain, rec.Type+" "+rec.Name)
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleRecordsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rec, err := s.Store.GetRecord(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "record not found")
		return
	}
	zone, err := s.Store.GetZone(r.Context(), rec.ZoneID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	var req recordReq
	if !readJSON(w, r, &req) {
		return
	}
	rec.Name, rec.Type, rec.TTL, rec.Priority, rec.Content, rec.Proxied =
		req.Name, req.Type, req.TTL, req.Priority, req.Content, req.Proxied
	if err := svc.ValidateRecord(rec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateRecord(r.Context(), rec); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleRecordsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rec, err := s.Store.GetRecord(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "record not found")
		return
	}
	zone, err := s.Store.GetZone(r.Context(), rec.ZoneID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "zone not found")
		return
	}
	if !s.canAccessZone(r, zone) {
		writeErr(w, http.StatusForbidden, "cannot access this zone")
		return
	}
	if err := s.Store.DeleteRecord(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- providers -------------------------------------------------------------------

func (s *Server) handleProvidersList(w http.ResponseWriter, r *http.Request) {
	provs, err := s.Store.ListProviders(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(provs))
	for _, p := range provs {
		out = append(out, map[string]interface{}{
			"id": p.ID, "name": p.Name, "label": p.Label, "email": p.Email,
			"enabled": p.Enabled, "config": p.Config, "created_at": p.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"plugins":   s.DNS.PluginNames(),
		"providers": out,
	})
}

type providerReq struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	APIKey  string `json:"api_key"`
	Email   string `json:"email"`
	Config  string `json:"config"`
	Enabled *bool  `json:"enabled"`
}

func (s *Server) handleProvidersCreate(w http.ResponseWriter, r *http.Request) {
	var req providerReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "provider name (plugin id) is required")
		return
	}
	if !sliceContains(s.DNS.PluginNames(), req.Name) {
		writeErr(w, http.StatusBadRequest, "unknown provider plugin (available: local, cloudflare)")
		return
	}
	if req.Name != "local" && req.APIKey == "" {
		writeErr(w, http.StatusBadRequest, "api_key is required for this provider")
		return
	}
	enc, err := s.Cipher.Encrypt(req.APIKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	p := &store.Provider{
		Name: req.Name, Label: req.Label, APIKeyEnc: enc, Email: req.Email,
		Config: req.Config, Enabled: enabled,
	}
	if p.Config == "" {
		p.Config = "{}"
	}
	if err := s.Store.CreateProvider(r.Context(), p); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "dns.provider-create", p.Name, "")
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id": p.ID, "name": p.Name, "label": p.Label, "email": p.Email, "enabled": p.Enabled,
	})
}

func (s *Server) handleProvidersUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.Store.GetProvider(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	var req providerReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Label != "" {
		p.Label = req.Label
	}
	if req.Email != "" {
		p.Email = req.Email
	}
	if req.Config != "" {
		p.Config = req.Config
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if req.APIKey != "" {
		enc, err := s.Cipher.Encrypt(req.APIKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.APIKeyEnc = enc
	}
	if err := s.Store.UpdateProvider(r.Context(), p); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id": p.ID, "name": p.Name, "label": p.Label, "email": p.Email, "enabled": p.Enabled,
	})
}

func (s *Server) handleProvidersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.DeleteProvider(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func sliceContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// primaryIP returns the server's primary non-loopback IPv4.
func (s *Server) primaryIP() string {
	return s.primaryIPv4
}

