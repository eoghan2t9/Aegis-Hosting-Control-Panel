package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Docker manages per-user Docker containers via the `docker` CLI — argv-only
// exec, never a shell string, the same "shell the real tool" pattern used
// everywhere else in svc. Containers run under the fixed name "aegis-c<id>"
// with a small, fixed set of docker-run flags derived entirely from
// validated, structured input (see runArgs): there is no free-form flags
// field, so there is no path from the API to --privileged, --network host,
// --pid host, or a docker.sock bind mount.
//
// Unlike every other package feature flag (mail, ftp, cron, ...), Docker
// access defaults OFF per package (see packages.allow_docker in store.go) —
// a container can consume host CPU/memory/disk well beyond what a chrooted
// account normally reaches, so an admin has to opt a package in explicitly.
type Docker struct {
	Cfg     *config.Config
	Store   *store.Store
	Files   *Files
	Domains *Domains
}

func NewDocker(cfg *config.Config, st *store.Store, files *Files, domains *Domains) *Docker {
	return &Docker{Cfg: cfg, Store: st, Files: files, Domains: domains}
}

const (
	// dockerPortRange bounds auto-allocated (and user-pinned) host ports to a
	// band well clear of every service this panel manages itself (22, 25,
	// 53, 80, 143, 443, 587, 993, 3306, 5432, 8080, ...) and of other
	// containers, checked live against both the containers table and an
	// actual bind attempt.
	dockerPortRangeStart = 20000
	dockerPortRangeEnd   = 29999
	maxPortsPerContainer = 10
	maxMemoryLimitMB     = 8192
	maxCPULimit          = 8.0
)

var (
	containerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,49}$`)
	envKeyRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	cpuLimitRe      = regexp.MustCompile(`^\d+(\.\d+)?$`)
	// Registry/namespace/name components with an optional :tag or
	// @sha256:digest. Rejects shell metacharacters by construction — moot
	// for exec.Command (argv, no shell), but it also rejects nonsense before
	// it ever reaches docker.
	imageRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*(/[a-zA-Z0-9][a-zA-Z0-9._-]*)*(:[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127})?(@sha256:[a-f0-9]{64})?$`)

	validRestartPolicies = map[string]bool{"no": true, "on-failure": true, "always": true, "unless-stopped": true}
)

func containerName(id int64) string { return fmt.Sprintf("aegis-c%d", id) }

// Installed reports whether the docker CLI exists on PATH.
func (dk *Docker) Installed() bool { return LookPath("docker") }

// Status reports install/daemon state for the admin UI's setup banner.
func (dk *Docker) Status() (installed, daemonUp bool, version string) {
	installed = dk.Installed()
	if !installed {
		return
	}
	out, err := RunTimeout(5*time.Second, "docker", "version", "--format", "{{.Server.Version}}")
	if err == nil && out != "" {
		daemonUp = true
		version = out
	}
	return
}

// Install installs Docker Engine from the distro repos and starts it.
// Admin-only at the API layer — this is a host-wide, security-relevant
// change, not a per-account provisioning step.
func (dk *Docker) Install() error {
	if err := installAptPackages("docker.io"); err != nil {
		return err
	}
	if systemdIsInit() {
		if _, err := RunTimeout(30*time.Second, "systemctl", "enable", "--now", "docker"); err != nil {
			return fmt.Errorf("enable docker service: %w", err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, up, _ := dk.Status(); up {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("docker installed but the daemon did not come up")
}

// CreateContainerRequest is the panel-facing shape for provisioning a
// container. Every field funnels into a fixed docker-run invocation — see
// the Docker type doc comment.
type CreateContainerRequest struct {
	Name          string              `json:"name"`
	Image         string              `json:"image"`
	Ports         []store.PortMap     `json:"ports"`
	Env           map[string]string   `json:"env"`
	Volumes       []store.VolumeMount `json:"volumes"`
	RestartPolicy string              `json:"restart_policy"`
	MemoryLimitMB int                 `json:"memory_limit_mb"`
	CPULimit      string              `json:"cpu_limit"`
	DomainID      int64               `json:"domain_id"`
	WebPort       int                 `json:"web_port"`
}

// validate checks and normalizes req into a store.Container, resolving
// volume host paths and allocating any unpinned host ports. It touches
// neither the database nor docker — Create does that once this succeeds.
func (dk *Docker) validate(ctx context.Context, user *store.User, req CreateContainerRequest) (*store.Container, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !containerNameRe.MatchString(name) {
		return nil, errors.New("name must be 2-50 lowercase letters, digits or hyphens, starting with a letter")
	}
	image := strings.TrimSpace(req.Image)
	if !imageRe.MatchString(image) {
		return nil, errors.New("invalid image reference")
	}
	if len(req.Ports) == 0 {
		return nil, errors.New("at least one port mapping is required")
	}
	if len(req.Ports) > maxPortsPerContainer {
		return nil, fmt.Errorf("at most %d port mappings", maxPortsPerContainer)
	}

	used, err := dk.usedHostPorts(ctx)
	if err != nil {
		return nil, err
	}
	seenContainerPorts := map[int]bool{}
	ports := make([]store.PortMap, 0, len(req.Ports))
	for _, p := range req.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return nil, fmt.Errorf("invalid container port %d", p.ContainerPort)
		}
		if seenContainerPorts[p.ContainerPort] {
			return nil, fmt.Errorf("container port %d listed twice", p.ContainerPort)
		}
		seenContainerPorts[p.ContainerPort] = true
		proto := strings.ToLower(strings.TrimSpace(p.Proto))
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			return nil, fmt.Errorf("proto must be tcp or udp, got %q", p.Proto)
		}
		hostPort := p.HostPort
		switch {
		case hostPort == 0:
			hp, err := dk.allocatePort(used)
			if err != nil {
				return nil, err
			}
			hostPort = hp
		case hostPort < dockerPortRangeStart || hostPort > dockerPortRangeEnd:
			return nil, fmt.Errorf("host port must be in %d-%d (or 0 to auto-allocate)", dockerPortRangeStart, dockerPortRangeEnd)
		case used[hostPort]:
			return nil, fmt.Errorf("host port %d is already in use by another container", hostPort)
		}
		used[hostPort] = true
		ports = append(ports, store.PortMap{ContainerPort: p.ContainerPort, HostPort: hostPort, Proto: proto, Public: p.Public})
	}

	env := map[string]string{}
	for k, v := range req.Env {
		k = strings.TrimSpace(k)
		if !envKeyRe.MatchString(k) {
			return nil, fmt.Errorf("invalid environment variable name %q", k)
		}
		env[k] = v
	}

	volumes := make([]store.VolumeMount, 0, len(req.Volumes))
	for _, v := range req.Volumes {
		cp := strings.TrimSpace(v.ContainerPath)
		if !strings.HasPrefix(cp, "/") || cp == "/" {
			return nil, fmt.Errorf("volume container path %q must be an absolute path, not /", v.ContainerPath)
		}
		if _, err := dk.Files.Resolve(user, v.HostPath); err != nil {
			return nil, fmt.Errorf("volume host path %q: %w", v.HostPath, err)
		}
		volumes = append(volumes, store.VolumeMount{HostPath: v.HostPath, ContainerPath: cp})
	}

	restart := strings.TrimSpace(req.RestartPolicy)
	if restart == "" {
		restart = "unless-stopped"
	}
	if !validRestartPolicies[restart] {
		return nil, fmt.Errorf("invalid restart policy %q", restart)
	}

	if req.MemoryLimitMB < 0 || req.MemoryLimitMB > maxMemoryLimitMB {
		return nil, fmt.Errorf("memory limit must be between 0 (unlimited) and %d MB", maxMemoryLimitMB)
	}
	cpuLimit := strings.TrimSpace(req.CPULimit)
	if cpuLimit != "" {
		if !cpuLimitRe.MatchString(cpuLimit) {
			return nil, errors.New("invalid cpu limit")
		}
		if f, _ := strconv.ParseFloat(cpuLimit, 64); f <= 0 || f > maxCPULimit {
			return nil, fmt.Errorf("cpu limit must be between 0 and %g", maxCPULimit)
		}
	}

	webPort := req.WebPort
	domainID := req.DomainID
	if domainID != 0 {
		dom, err := dk.Store.GetDomain(ctx, domainID)
		if err != nil {
			return nil, fmt.Errorf("domain: %w", err)
		}
		if dom.UserID != user.ID {
			return nil, errors.New("you do not own that domain")
		}
		if webPort == 0 {
			return nil, errors.New("web_port is required when attaching to a domain")
		}
		if !seenContainerPorts[webPort] {
			return nil, fmt.Errorf("web_port %d must be one of the published container ports", webPort)
		}
		existing, err := dk.Store.ListContainers(ctx, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range existing {
			if c.DomainID == domainID {
				return nil, fmt.Errorf("domain already has a container attached (%s)", c.Name)
			}
		}
	} else {
		webPort = 0
	}

	return &store.Container{
		UserID:        user.ID,
		DomainID:      domainID,
		Name:          name,
		Image:         image,
		Ports:         ports,
		WebPort:       webPort,
		Env:           env,
		Volumes:       volumes,
		RestartPolicy: restart,
		MemoryLimitMB: req.MemoryLimitMB,
		CPULimit:      cpuLimit,
	}, nil
}

func (dk *Docker) usedHostPorts(ctx context.Context) (map[int]bool, error) {
	all, err := dk.Store.ListContainers(ctx, 0)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, c := range all {
		for _, p := range c.Ports {
			used[p.HostPort] = true
		}
	}
	return used, nil
}

func (dk *Docker) allocatePort(used map[int]bool) (int, error) {
	for p := dockerPortRangeStart; p <= dockerPortRangeEnd; p++ {
		if used[p] {
			continue
		}
		if portFree(p) {
			return p, nil
		}
	}
	return 0, errors.New("no free ports in the container port range")
}

func portFree(p int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Create validates req, provisions volume directories, records the
// container, and starts it. Any failure after the DB insert rolls the row
// back, so a failed create never leaves an orphaned record.
func (dk *Docker) Create(ctx context.Context, user *store.User, req CreateContainerRequest) (*store.Container, error) {
	if !dk.Installed() {
		return nil, errors.New("docker is not installed on this host — an admin can install it from the Containers page")
	}
	pkg, err := dk.Store.GetPackage(ctx, user.PackageID)
	if err != nil {
		pkg, _ = dk.Store.GetDefaultPackage(ctx)
	}
	if pkg != nil && pkg.MaxContainers > 0 {
		n, err := dk.Store.CountContainers(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if n >= pkg.MaxContainers {
			return nil, fmt.Errorf("package %s allows at most %d container(s)", pkg.Name, pkg.MaxContainers)
		}
	}

	c, err := dk.validate(ctx, user, req)
	if err != nil {
		return nil, err
	}
	if err := dk.Store.CreateContainer(ctx, c); err != nil {
		return nil, err
	}
	for _, v := range c.Volumes {
		if abs, err := dk.Files.Resolve(user, v.HostPath); err == nil {
			_ = os.MkdirAll(abs, 0o755)
			_, _ = RunTimeout(10*time.Second, "chown", "-R", user.Username+":www-data", abs)
		}
	}
	if err := dk.dockerRun(ctx, user, c); err != nil {
		_ = dk.Store.DeleteContainer(ctx, c.ID)
		return nil, err
	}
	if err := dk.applyDomainProxy(ctx, c); err != nil {
		return nil, fmt.Errorf("container started, but attaching it to the domain failed: %w", err)
	}
	c.Status = "running"
	return c, nil
}

// runArgs builds the fixed docker-run invocation for c. Every value traces
// back to a field validated in validate(): there is no path from user input
// to an arbitrary docker flag, --privileged, or a host-namespace/socket
// mount.
func (dk *Docker) runArgs(user *store.User, c *store.Container) ([]string, error) {
	args := []string{"run", "-d",
		"--name", containerName(c.ID),
		"--restart", c.RestartPolicy,
		"--pids-limit", "512",
		"--security-opt", "no-new-privileges",
		"--label", "aegis.managed=true",
		"--label", fmt.Sprintf("aegis.container_id=%d", c.ID),
		"--label", fmt.Sprintf("aegis.user_id=%d", c.UserID),
	}
	if c.MemoryLimitMB > 0 {
		mem := fmt.Sprintf("%dm", c.MemoryLimitMB)
		args = append(args, "--memory", mem, "--memory-swap", mem)
	}
	if c.CPULimit != "" {
		args = append(args, "--cpus", c.CPULimit)
	}
	for _, p := range c.Ports {
		bindIP := "127.0.0.1"
		if p.Public {
			bindIP = "0.0.0.0"
		}
		args = append(args, "-p", fmt.Sprintf("%s:%d:%d/%s", bindIP, p.HostPort, p.ContainerPort, p.Proto))
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic argv, easier to debug from the audit log
	for _, k := range keys {
		args = append(args, "-e", k+"="+c.Env[k])
	}
	for _, v := range c.Volumes {
		abs, err := dk.Files.Resolve(user, v.HostPath)
		if err != nil {
			return nil, err
		}
		args = append(args, "-v", abs+":"+v.ContainerPath)
	}
	args = append(args, c.Image)
	return args, nil
}

func (dk *Docker) dockerRun(ctx context.Context, user *store.User, c *store.Container) error {
	args, err := dk.runArgs(user, c)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute) // first run may need to pull the image
	defer cancel()
	if _, err := Exec(cctx, "docker", args...); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	return nil
}

// owner resolves the store.User that owns c, for operations (Recreate) that
// need it again after the initial create request is long gone.
func (dk *Docker) owner(ctx context.Context, c *store.Container) (*store.User, error) {
	return dk.Store.GetUserByID(ctx, c.UserID)
}

// Start starts a stopped container and, if attached to a domain, re-points
// the vhost at it.
func (dk *Docker) Start(ctx context.Context, c *store.Container) error {
	if _, err := RunTimeout(30*time.Second, "docker", "start", containerName(c.ID)); err != nil {
		return fmt.Errorf("docker start: %w", err)
	}
	return dk.applyDomainProxy(ctx, c)
}

// Stop stops the container and, if attached, reverts the domain to serving
// its normal docroot (a styled placeholder, or PHP) instead of a dead proxy.
func (dk *Docker) Stop(ctx context.Context, c *store.Container) error {
	if _, err := RunTimeout(30*time.Second, "docker", "stop", containerName(c.ID)); err != nil {
		return fmt.Errorf("docker stop: %w", err)
	}
	return dk.clearDomainProxy(ctx, c.DomainID)
}

// Restart restarts the container in place; the domain proxy target (a fixed
// host:port) never changes across a restart, so no vhost re-apply is needed.
func (dk *Docker) Restart(ctx context.Context, c *store.Container) error {
	if _, err := RunTimeout(60*time.Second, "docker", "restart", containerName(c.ID)); err != nil {
		return fmt.Errorf("docker restart: %w", err)
	}
	return nil
}

// Recreate pulls the latest image and replaces the running container with a
// fresh one using the same stored configuration — the normal way to pick up
// a new image tag, since docker doesn't support swapping a running
// container's image in place.
func (dk *Docker) Recreate(ctx context.Context, c *store.Container) error {
	user, err := dk.owner(ctx, c)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := Exec(pctx, "docker", "pull", c.Image); err != nil {
		return fmt.Errorf("docker pull: %w", err)
	}
	_, _ = RunTimeout(30*time.Second, "docker", "rm", "-f", containerName(c.ID))
	if err := dk.dockerRun(ctx, user, c); err != nil {
		return err
	}
	return dk.applyDomainProxy(ctx, c)
}

// Delete stops and removes the container, detaches it from any domain, and
// deletes its record. Volume directories under the owner's home are left in
// place (like domain document roots on domain delete) — the caller decides
// whether to clean those up separately via the file manager.
func (dk *Docker) Delete(ctx context.Context, c *store.Container) error {
	_, _ = RunTimeout(30*time.Second, "docker", "rm", "-f", containerName(c.ID))
	if err := dk.clearDomainProxy(ctx, c.DomainID); err != nil {
		return err
	}
	return dk.Store.DeleteContainer(ctx, c.ID)
}

// applyDomainProxy points the attached domain's vhost at c's published web
// port. No-op when c isn't attached to a domain.
func (dk *Docker) applyDomainProxy(ctx context.Context, c *store.Container) error {
	if c.DomainID == 0 || c.WebPort == 0 {
		return nil
	}
	hostPort := 0
	for _, p := range c.Ports {
		if p.ContainerPort == c.WebPort {
			hostPort = p.HostPort
			break
		}
	}
	if hostPort == 0 {
		return fmt.Errorf("web_port %d is not among this container's published ports", c.WebPort)
	}
	if err := dk.Store.SetDomainProxyTarget(ctx, c.DomainID, fmt.Sprintf("127.0.0.1:%d", hostPort)); err != nil {
		return err
	}
	return dk.Domains.Apply(ctx, c.DomainID)
}

// clearDomainProxy reverts a domain to its normal (non-proxied) serving path.
func (dk *Docker) clearDomainProxy(ctx context.Context, domainID int64) error {
	if domainID == 0 {
		return nil
	}
	if err := dk.Store.SetDomainProxyTarget(ctx, domainID, ""); err != nil {
		return err
	}
	return dk.Domains.Apply(ctx, domainID)
}

// List returns userID's containers (or everyone's when userID is 0, for
// admins) with their live docker state merged in.
func (dk *Docker) List(ctx context.Context, userID int64) ([]*store.Container, error) {
	rows, err := dk.Store.ListContainers(ctx, userID)
	if err != nil {
		return nil, err
	}
	dk.annotateStatus(rows)
	return rows, nil
}

func (dk *Docker) Get(ctx context.Context, id int64) (*store.Container, error) {
	c, err := dk.Store.GetContainer(ctx, id)
	if err != nil {
		return nil, err
	}
	dk.annotateStatus([]*store.Container{c})
	return c, nil
}

// annotateStatus fills in Status from one `docker ps -a` call (rather than
// one `docker inspect` per row) so a containers list with many rows stays a
// single subprocess.
func (dk *Docker) annotateStatus(rows []*store.Container) {
	if len(rows) == 0 || !dk.Installed() {
		return
	}
	out, err := RunTimeout(10*time.Second, "docker", "ps", "-a",
		"--filter", "label=aegis.managed=true",
		"--format", `{{.Names}}\t{{.State}}`)
	if err != nil {
		for _, c := range rows {
			c.Status = "unknown"
		}
		return
	}
	states := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			states[parts[0]] = parts[1]
		}
	}
	for _, c := range rows {
		if st, ok := states[containerName(c.ID)]; ok {
			c.Status = st
		} else {
			c.Status = "not_created"
		}
	}
}

// Logs returns the last tail lines of combined stdout/stderr.
func (dk *Docker) Logs(ctx context.Context, c *store.Container, tail int) (string, error) {
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := Exec(cctx, "docker", "logs", "--tail", strconv.Itoa(tail), containerName(c.ID))
	if err != nil {
		return "", fmt.Errorf("docker logs: %w", err)
	}
	return out, nil
}

// ContainerStats is a point-in-time CPU/memory reading (docker stats --no-stream).
type ContainerStats struct {
	CPUPercent string `json:"cpu_percent"`
	MemUsage   string `json:"mem_usage"`
	MemPercent string `json:"mem_percent"`
	NetIO      string `json:"net_io"`
}

func (dk *Docker) Stats(ctx context.Context, c *store.Container) (ContainerStats, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := Exec(cctx, "docker", "stats", "--no-stream", "--format", "{{json .}}", containerName(c.ID))
	if err != nil {
		return ContainerStats{}, fmt.Errorf("docker stats: %w", err)
	}
	var raw struct {
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
		MemPerc  string `json:"MemPerc"`
		NetIO    string `json:"NetIO"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return ContainerStats{}, fmt.Errorf("parse docker stats: %w", err)
	}
	return ContainerStats{CPUPercent: raw.CPUPerc, MemUsage: raw.MemUsage, MemPercent: raw.MemPerc, NetIO: raw.NetIO}, nil
}
