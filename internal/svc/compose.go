package svc

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"aegis/internal/store"
	"go.yaml.in/yaml/v3"
)

// composeFile is the minimal subset of a docker-compose.yml this importer
// understands — just enough to prefill the containers "New container" form
// (see ParseCompose / handleContainersImportCompose). Anything it can't
// confidently translate is reported in ComposeImportResult.Warnings instead
// of silently dropped or failing the whole import.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image         string `yaml:"image"`
	ContainerName string `yaml:"container_name"`
	Ports         []any  `yaml:"ports"`
	Environment   any    `yaml:"environment"` // a list of "K=V" strings, or a map
	Volumes       []any  `yaml:"volumes"`
	Restart       string `yaml:"restart"`
}

// ComposeImportResult mirrors CreateContainerRequest's shape (so the
// frontend can drop it straight into the "New container" form) plus
// import-specific bookkeeping. Volume HostPaths are already remapped to
// "docker/<name>/<subpath>" — a home-relative path Docker.Create() will
// create (and chown) on disk the moment the container is actually created,
// exactly like any other volume path (see docker.go's existing per-volume
// os.MkdirAll in Create()); nothing here touches the filesystem.
type ComposeImportResult struct {
	// NeedsService is set instead of guessing when the compose file declares
	// more than one service and the caller didn't pick one yet — Services
	// lists the names so the frontend can ask.
	NeedsService bool     `json:"needs_service,omitempty"`
	Services     []string `json:"services,omitempty"`

	Name          string              `json:"name,omitempty"`
	Image         string              `json:"image,omitempty"`
	Ports         []store.PortMap     `json:"ports,omitempty"`
	Env           map[string]string   `json:"env,omitempty"`
	Volumes       []store.VolumeMount `json:"volumes,omitempty"`
	RestartPolicy string              `json:"restart_policy,omitempty"`
	// Warnings lists compose constructs this importer couldn't confidently
	// translate (long-form ports/volumes, build-only services, ...) so the
	// user knows what to add by hand instead of silently losing it.
	Warnings []string `json:"warnings,omitempty"`
}

var composeNameSanitizeRe = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeContainerName best-effort adapts a compose service/container name
// to Aegis's containerNameRe (^[a-z][a-z0-9-]{1,49}$, see docker.go) — a
// prefill convenience only; Docker.validate() re-checks for real on create.
func sanitizeContainerName(s string) string {
	s = composeNameSanitizeRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "app"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "c-" + s
	}
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return s
}

// ParseCompose parses a docker-compose.yml (or a subset of one) into an
// import preview for the given service. If the file declares more than one
// service and none is named, NeedsService is set instead of guessing.
func ParseCompose(yamlText string, service string) (*ComposeImportResult, error) {
	var cf composeFile
	if err := yaml.Unmarshal([]byte(yamlText), &cf); err != nil {
		return nil, fmt.Errorf("invalid compose file: %w", err)
	}
	if len(cf.Services) == 0 {
		return nil, errors.New("no services found in compose file")
	}
	if service == "" {
		if len(cf.Services) > 1 {
			names := make([]string, 0, len(cf.Services))
			for n := range cf.Services {
				names = append(names, n)
			}
			sort.Strings(names)
			return &ComposeImportResult{NeedsService: true, Services: names}, nil
		}
		for n := range cf.Services {
			service = n
		}
	}
	svcDef, ok := cf.Services[service]
	if !ok {
		return nil, fmt.Errorf("service %q not found in compose file", service)
	}

	name := svcDef.ContainerName
	if name == "" {
		name = service
	}
	name = sanitizeContainerName(name)

	result := &ComposeImportResult{
		Name:          name,
		Image:         strings.TrimSpace(svcDef.Image),
		RestartPolicy: normalizeComposeRestart(svcDef.Restart),
	}
	if result.Image == "" {
		result.Warnings = append(result.Warnings, "service has no image (build-only services aren't supported — set an image manually)")
	}

	ports, warnings := parseComposePorts(svcDef.Ports)
	result.Ports = ports
	result.Warnings = append(result.Warnings, warnings...)

	env, warnings := parseComposeEnv(svcDef.Environment)
	result.Env = env
	result.Warnings = append(result.Warnings, warnings...)

	volumes, warnings := parseComposeVolumes(svcDef.Volumes, name)
	result.Volumes = volumes
	result.Warnings = append(result.Warnings, warnings...)

	return result, nil
}

// normalizeComposeRestart maps compose's restart: value onto Aegis's
// restart-policy vocabulary, which happens to already match 1:1
// (validRestartPolicies is declared in docker.go, same package).
func normalizeComposeRestart(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	if validRestartPolicies[r] {
		return r
	}
	return "unless-stopped"
}

// composePortString accepts compose's short port syntax as either a YAML
// string or a bare integer; anything else (long-form maps) is unsupported.
func composePortString(item any) (string, bool) {
	switch v := item.(type) {
	case string:
		return v, true
	case int:
		return strconv.Itoa(v), true
	default:
		return "", false
	}
}

func parseComposePorts(raw []any) ([]store.PortMap, []string) {
	var out []store.PortMap
	var warnings []string
	for _, item := range raw {
		spec, ok := composePortString(item)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipped a port entry in an unsupported format (long syntax isn't supported — add it manually): %v", item))
			continue
		}
		proto := "tcp"
		if i := strings.LastIndex(spec, "/"); i >= 0 {
			proto = strings.ToLower(spec[i+1:])
			spec = spec[:i]
		}
		var hostPort, containerPort int
		if i := strings.LastIndex(spec, ":"); i >= 0 {
			hostPort, _ = strconv.Atoi(strings.TrimSpace(spec[:i]))
			containerPort, _ = strconv.Atoi(strings.TrimSpace(spec[i+1:]))
		} else {
			containerPort, _ = strconv.Atoi(strings.TrimSpace(spec))
		}
		if containerPort == 0 {
			warnings = append(warnings, fmt.Sprintf("skipped unparseable port entry %q", spec))
			continue
		}
		out = append(out, store.PortMap{ContainerPort: containerPort, HostPort: hostPort, Proto: proto})
	}
	return out, warnings
}

func parseComposeEnv(raw any) (map[string]string, []string) {
	env := map[string]string{}
	var warnings []string
	switch v := raw.(type) {
	case nil:
		// no environment section
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("skipped an environment entry in an unsupported format: %v", item))
				continue
			}
			if i := strings.Index(s, "="); i > 0 {
				env[strings.TrimSpace(s[:i])] = s[i+1:]
			} else {
				warnings = append(warnings, fmt.Sprintf("skipped environment entry %q (expected KEY=VALUE)", s))
			}
		}
	case map[string]any:
		for k, val := range v {
			env[strings.TrimSpace(k)] = fmt.Sprintf("%v", val)
		}
	default:
		warnings = append(warnings, "skipped environment section in an unsupported format")
	}
	return env, warnings
}

// parseComposeVolumes translates compose's short volume syntax
// ("HOST:CONTAINER[:MODE]" or a bare "CONTAINER_PATH" anonymous mount) into
// Aegis VolumeMounts, remapping every HostPath into the owning user's
// "docker/<containerName>/<subpath>" home-relative directory regardless of
// what the original host path was (absolute bind path, relative path, or
// named volume) — see composeVolumeSubpath. Long-form (map) entries are
// reported as warnings instead of guessed at.
func parseComposeVolumes(raw []any, containerName string) ([]store.VolumeMount, []string) {
	var out []store.VolumeMount
	var warnings []string
	used := map[string]bool{}
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipped a volume entry in an unsupported format (long syntax isn't supported — add it manually): %v", item))
			continue
		}
		parts := strings.Split(s, ":")
		var hostRaw, containerPath string
		if len(parts) == 1 {
			containerPath = parts[0] // anonymous volume — no host path in the compose file
		} else {
			hostRaw = parts[0]
			containerPath = parts[1]
			// parts[2], if present (e.g. "ro"), is the mount mode — Aegis
			// volumes are always read-write, so it's intentionally dropped.
		}
		containerPath = strings.TrimSpace(containerPath)
		if containerPath == "" {
			warnings = append(warnings, fmt.Sprintf("skipped unparseable volume entry %q", s))
			continue
		}
		sub := composeVolumeSubpath(hostRaw, containerPath)
		orig, n := sub, 2
		for used[sub] {
			sub = fmt.Sprintf("%s-%d", orig, n)
			n++
		}
		used[sub] = true
		out = append(out, store.VolumeMount{
			HostPath:      "docker/" + containerName + "/" + sub,
			ContainerPath: containerPath,
		})
	}
	return out, warnings
}

// composeVolumeSubpath derives a short, filesystem-safe folder name for a
// volume from its original compose host path (bind mount path or named
// volume name), falling back to the container path's basename.
func composeVolumeSubpath(hostRaw, containerPath string) string {
	base := strings.TrimSuffix(hostRaw, "/")
	if base == "" || base == "." {
		base = containerPath
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = composeNameSanitizeRe.ReplaceAllString(strings.ToLower(base), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "data"
	}
	return base
}
