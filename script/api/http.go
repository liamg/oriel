package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/script"
)

//oriel:api name=http cap=target
type HTTP struct{ a *APIs }

//oriel:api doc="Make an HTTP request off the loop, and call fn(res, err) on it when it's answered: res is { status, body, headers } (headers by lower-case name), err why it failed. spec: { method (GET by default), url, headers = { Name = value }, body (a string: oriel.json(t) for JSON), auth, url_secret, timeout (seconds, 30 by default) }. The host must be in the plugin's net grant. A secret is named, never read: auth = 'NAME' sends it as a bearer token, url_secret = 'NAME' is a secret that is the whole URL (an incoming webhook, in place of url); each goes only to the host its secrets grant binds it to, over https, and no answer or error shows it."
func (h HTTP) Request(spec script.Table, fn *script.Fn) error {
	a := h.a
	if fn == nil {
		return errors.New("http.request needs a function for its answer")
	}
	r := ext.HTTPRequest{Method: str(spec, "method"), URL: str(spec, "url"), Body: str(spec, "body"), Auth: str(spec, "auth"),
		URLSecret: str(spec, "url_secret"), Headers: map[string]string{}}
	for k, v := range tableOf(spec["headers"]) {
		r.Headers[k] = fmt.Sprint(v)
	}
	if t, ok := spec["timeout"].(float64); ok {
		r.Timeout = t
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	ms := a.methods()
	go func() {
		v, err := ms.Call(context.Background(), a.Plugin, "http.request", raw)
		a.Host.Do(func() {
			if err != nil {
				a.VM.Call(fn, nil, err.Error())
				return
			}
			res := v.(*ext.HTTPResponse)
			headers := script.Table{}
			for k, v := range res.Headers {
				headers[k] = v
			}
			a.VM.Call(fn, script.Table{"status": float64(res.Status), "body": res.Body, "headers": headers})
		})
	}()
	return nil
}
