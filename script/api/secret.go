package api

import "github.com/liamg/oriel/plugin"

//oriel:api name=secret cap=target
type Secret struct{ a *APIs }

//oriel:api doc="Whether a secret the plugin declares (permissions.secrets) is set: a bool, never its value. Use it to keep the plugin's entries hidden until its key is set: shown = function() return oriel.secret.has('LINEAR_TOKEN') end."
func (s Secret) Has(name string) (bool, error) {
	if err := s.a.Check(plugin.CapSecrets, name); err != nil {
		return false, err
	}
	_, ok := s.a.Host.Secrets().Secret(name)
	return ok, nil
}
