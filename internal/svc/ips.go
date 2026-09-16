package svc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"aegis/internal/store"
)

// IPs manages the panel's pool of server-owned IP addresses and their
// assignment to domains. An address must already be configured on a local
// network interface before it can be added to the pool — the whole point of
// "dedicated IP" hosting is a vhost binding to that literal socket, and a
// vhost bound to an address the box doesn't actually own fails outright
// (nginx -t / caddy validate reject it), so Create refuses it up front
// instead of letting an admin discover that only at Apply time.
type IPs struct {
	Store   *store.Store
	Domains *Domains

	// assignMu serializes the dedicated-IP check-then-set in Assign, the
	// same TOCTOU-safety pattern used for Docker.Create: two concurrent
	// assigns of the same dedicated IP to different domains could otherwise
	// both read "unassigned" before either writes.
	assignMu sync.Mutex
}

func NewIPs(st *store.Store, domains *Domains) *IPs {
	return &IPs{Store: st, Domains: domains}
}

// DetectLocalIPs lists every non-loopback, non-link-local IPv4/IPv6 address
// currently configured on a local network interface — used both to help an
// admin pick a real address when adding one to the pool, and to validate one
// before Create accepts it.
func DetectLocalIPs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() || ipNet.IP.IsLinkLocalMulticast() {
			continue
		}
		out = append(out, ipNet.IP.String())
	}
	return out, nil
}

// localIPSet returns DetectLocalIPs as a lookup set; a detection failure is
// treated as "nothing verified" (empty set) rather than fatal.
func localIPSet() map[string]bool {
	addrs, _ := DetectLocalIPs()
	set := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		set[a] = true
	}
	return set
}

// DetectPublicIP returns this host's outbound-facing address by opening a
// UDP "connection" to a public address and reading back the local endpoint
// the kernel would route it from — no packet is actually sent (UDP dial only
// resolves a route), so this works even fully offline against a
// directly-connected gateway. Best-effort: "" on any failure (e.g. no
// default route at all), which callers show as "unknown" instead of failing
// the whole page.
func DetectPublicIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return ""
	}
	return host
}

// DetectPrimaryIP returns the server's primary non-loopback IPv4 address —
// the first one found on the first up, non-loopback interface. Used as the
// default target for auto-created DNS A records (Domains.Create) and for the
// panel's own "server IP" display; "" on any detection failure.
func DetectPrimaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	return ""
}

func (ips *IPs) List(ctx context.Context) ([]*store.IP, error) {
	return ips.Store.ListIPs(ctx)
}

// Create adds address to the pool after confirming it's actually bound to a
// local interface (see the type doc) — a fake or aspirational IP would only
// break the first domain assigned to it.
func (ips *IPs) Create(ctx context.Context, address, label, kind string) (*store.IP, error) {
	address = strings.TrimSpace(address)
	parsed := net.ParseIP(address)
	if parsed == nil {
		return nil, fmt.Errorf("%q is not a valid IP address", address)
	}
	address = parsed.String()
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = store.IPKindShared
	}
	if kind != store.IPKindShared && kind != store.IPKindDedicated {
		return nil, fmt.Errorf("kind must be %q or %q", store.IPKindShared, store.IPKindDedicated)
	}
	if !localIPSet()[address] {
		return nil, fmt.Errorf("%s is not configured on any network interface on this host — add it "+
			"(e.g. `ip addr add %s/32 dev eth0`) before adding it to the pool, or vhosts bound to it will fail to start", address, address)
	}
	ip := &store.IP{Address: address, Label: strings.TrimSpace(label), Kind: kind}
	if err := ips.Store.CreateIP(ctx, ip); err != nil {
		return nil, err
	}
	return ip, nil
}

// Update changes label/kind. Switching to "dedicated" is refused while more
// than one domain already uses this IP — dedicated means exactly one.
func (ips *IPs) Update(ctx context.Context, id int64, label, kind string) (*store.IP, error) {
	ip, err := ips.Store.GetIP(ctx, id)
	if err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	if kind != store.IPKindShared && kind != store.IPKindDedicated {
		return nil, fmt.Errorf("kind must be %q or %q", store.IPKindShared, store.IPKindDedicated)
	}
	if kind == store.IPKindDedicated && ip.Kind != store.IPKindDedicated {
		n, err := ips.Store.CountDomainsUsingIP(ctx, id)
		if err != nil {
			return nil, err
		}
		if n > 1 {
			return nil, fmt.Errorf("cannot mark dedicated: %d domains already use this IP — unassign all but one first", n)
		}
	}
	ip.Label, ip.Kind = strings.TrimSpace(label), kind
	if err := ips.Store.UpdateIP(ctx, ip); err != nil {
		return nil, err
	}
	return ip, nil
}

// Delete removes the pool entry. Refuses while any domain still references
// it — those domains would otherwise keep a dangling ip_id pointing at
// nothing, and their vhost's bound address would no longer be tracked.
func (ips *IPs) Delete(ctx context.Context, id int64) error {
	n, err := ips.Store.CountDomainsUsingIP(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%d domain(s) still use this IP — unassign them first", n)
	}
	return ips.Store.DeleteIP(ctx, id)
}

// Assign points domainID's vhost at ipID (0 clears the assignment back to
// the wildcard address) and regenerates its web server config immediately.
func (ips *IPs) Assign(ctx context.Context, domainID, ipID int64) error {
	ips.assignMu.Lock()
	err := ips.assignLocked(ctx, domainID, ipID)
	ips.assignMu.Unlock()
	if err != nil {
		return err
	}
	return ips.Domains.Apply(ctx, domainID)
}

func (ips *IPs) assignLocked(ctx context.Context, domainID, ipID int64) error {
	if ipID != 0 {
		ip, err := ips.Store.GetIP(ctx, ipID)
		if err != nil {
			return fmt.Errorf("ip: %w", err)
		}
		if ip.Kind == store.IPKindDedicated {
			existing, err := ips.Store.DomainsUsingIP(ctx, ipID)
			if err != nil {
				return err
			}
			for _, d := range existing {
				if d.ID != domainID {
					return fmt.Errorf("%s is dedicated and already assigned to %s", ip.Address, d.Domain)
				}
			}
		}
	}
	return ips.Store.SetDomainIP(ctx, domainID, ipID)
}
