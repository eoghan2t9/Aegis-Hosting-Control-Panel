package api

import (
	"context"
	"net/http"

	"aegis/internal/store"
)

// Feature names as used in route gates and the frontend's can() helper.
const (
	FeatureSSL       = "ssl"
	FeatureDNS       = "dns"
	FeatureTerminal  = "terminal"
	FeatureBackups   = "backups"
	FeatureMail      = "mail"
	FeatureWebmail   = "webmail"
	FeatureDatabases = "databases"
	FeatureFiles     = "files"
	FeatureFTP       = "ftp"
	FeatureCron      = "cron"
	FeatureDocker    = "docker"
)

// packageFor returns the user's assigned package, falling back to the
// default package when the assignment is missing/dangling. It returns nil
// (no error) when there is no package at all — admins and freshly seeded
// databases can hit that path, and every gate then treats features as
// granted (same open-by-default behaviour the existing pkgAllows* had).
func (s *Server) packageFor(ctx context.Context, u *store.User) *store.Package {
	if u.PackageID > 0 {
		if pkg, err := s.Store.GetPackage(ctx, u.PackageID); err == nil {
			return pkg
		}
	}
	pkg, err := s.Store.GetDefaultPackage(ctx)
	if err != nil {
		return nil
	}
	return pkg
}

// packageAllows checks one feature flag on the user's package. Admins and
// resellers always pass (they manage the server, not a plan), and so does
// anyone without a package at all.
func (s *Server) packageAllows(u *store.User, flag bool) bool {
	if u.Role == store.RoleAdmin || u.Role == store.RoleReseller {
		return true
	}
	pkg := s.packageFor(context.Background(), u)
	if pkg == nil {
		return true
	}
	return flag
}

// featuresFor maps a user's package onto the feature set the frontend and
// route gates consume. Distinguishes "flag explicitly false" (deny) from
// "no package / admin" (allow) so plan-less users keep full access.
func (s *Server) featuresFor(ctx context.Context, u *store.User) map[string]bool {
	out := map[string]bool{}
	if u.Role == store.RoleAdmin || u.Role == store.RoleReseller {
		for _, f := range []string{
			FeatureSSL, FeatureDNS, FeatureTerminal, FeatureBackups,
			FeatureMail, FeatureWebmail, FeatureDatabases, FeatureFiles,
			FeatureFTP, FeatureCron, FeatureDocker,
		} {
			out[f] = true
		}
		return out
	}
	pkg := s.packageFor(ctx, u)
	if pkg == nil {
		for _, f := range []string{
			FeatureSSL, FeatureDNS, FeatureTerminal, FeatureBackups,
			FeatureMail, FeatureWebmail, FeatureDatabases, FeatureFiles,
			FeatureFTP, FeatureCron, FeatureDocker,
		} {
			out[f] = true
		}
		return out
	}
	out[FeatureSSL] = pkg.AllowSSL
	out[FeatureDNS] = pkg.AllowDNS
	out[FeatureTerminal] = pkg.AllowTerminal
	out[FeatureBackups] = pkg.AllowBackups
	out[FeatureMail] = pkg.AllowMail
	out[FeatureWebmail] = pkg.AllowWebmail
	out[FeatureDatabases] = pkg.AllowDatabases
	out[FeatureFiles] = pkg.AllowFiles
	out[FeatureFTP] = pkg.AllowFTP
	out[FeatureCron] = pkg.AllowCron
	out[FeatureDocker] = pkg.AllowDocker
	return out
}

// withFeature wraps a handler so it's only reachable when the acting user's
// package grants the named panel area. Route-level gate: keep per-handler
// ownership checks (canAccessZone, canManageUser, ...) — this only answers
// "does your plan include this area at all".
func (s *Server) withFeature(feature string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		if !s.packageAllows(u, s.featuresFor(r.Context(), u)[feature]) {
			writeErr(w, http.StatusForbidden, "your package does not include access to this feature")
			return
		}
		next(w, r)
	}
}
