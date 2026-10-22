package shell

import (
	"net/http"
	"path/filepath"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"

	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/plugin"
)

func (h *pluginHost) Net() *http.Client {
	if h.sh.cfg.Net != nil {
		return h.sh.cfg.Net
	}
	return http.DefaultClient
}

func (h *pluginHost) Secrets() plugin.Secrets { return h.sh.secretStore() }

// secretStore is the config's secrets, or secrets.json in the state directory; each value it hands out is
// redacted from the logs.
func (sh *shell) secretStore() plugin.Secrets {
	sh.secretsOnce.Do(func() {
		s := sh.cfg.Secrets
		if s == nil {
			dir := sh.cfg.StateDir
			if dir == "" {
				dir = logs.StateDir()
			}
			s = &plugin.SecretFile{Path: filepath.Join(dir, "secrets.json")}
		}
		var r *logs.Redactor
		if sh.cfg.Logs != nil {
			r = sh.cfg.Logs.Redact
		}
		sh.secrets = redacted{siteSecrets{s, sh.cfg.Registry}, r}
	})
	return sh.secrets
}

// siteSecrets are the secrets plugins and the app read: those set, and a code host's site's token from wherever it's
// found (kit.SiteTokens).
type siteSecrets struct {
	plugin.Secrets
	r registry.Registry
}

func (s siteSecrets) Secret(name string) (string, bool) {
	if t, ok := registry.ServiceOf[kit.SiteTokens](s.r, kit.SiteTokensService); ok {
		return t.Token(name, s.Secrets)
	}
	return s.Secrets.Secret(name)
}

func (s siteSecrets) SetSecret(name, value string) error {
	if t, ok := registry.ServiceOf[kit.SiteTokens](s.r, kit.SiteTokensService); ok {
		t.Forget(name)
	}
	return s.Secrets.SetSecret(name, value)
}

type redacted struct {
	plugin.Secrets
	r *logs.Redactor
}

func (s redacted) Secret(name string) (string, bool) {
	v, ok := s.Secrets.Secret(name)
	if ok && s.r != nil {
		s.r.Add(v)
	}
	return v, ok
}
