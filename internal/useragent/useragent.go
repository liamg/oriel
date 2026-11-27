// Package useragent is the User-Agent Oriel names itself with to other people's servers.
package useragent

import "net/http"

// Value is Oriel's User-Agent. Some hosts (Wikimedia) refuse Go's default one.
const Value = "Oriel (+https://github.com/liamg/oriel)"

// Set gives r Oriel's User-Agent, unless it has one already.
func Set(r *http.Request) {
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", Value)
	}
}
