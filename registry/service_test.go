package registry_test

import (
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// A service is found by name as the type it's used as; a later one of the name shadows it until it's disposed.
func TestServices(t *testing.T) {
	r := registry.New()
	if _, ok := registry.SessionsOf(r); ok {
		t.Fatal("a service no one registered")
	}
	a := &core.SessionState{ID: "a"}
	r.Contribute(registry.Service{Name: registry.SessionsService, Impl: testSessions{a}})
	b := &core.SessionState{ID: "b"}
	off := r.Contribute(registry.Service{Name: registry.SessionsService, Impl: testSessions{b}})
	if s, ok := registry.SessionsOf(r); !ok {
		t.Fatal("no Sessions")
	} else if se, _ := s.Front(); se.ID != "b" {
		t.Errorf("front = %q, want the later service's", se.ID)
	}
	off.Dispose()
	if se, _ := registry.FrontSession(r); se.ID != "a" {
		t.Errorf("front = %q once the later one is gone", se.ID)
	}
	r.Contribute(registry.Service{Name: "other", Impl: "not sessions"})
	if _, ok := registry.ServiceOf[registry.Sessions](r, "other"); ok {
		t.Error("a service of the wrong type")
	}
}

// A command's session is its {id}, else the one in front.
func TestSessionFromArgs(t *testing.T) {
	s := testSessions{&core.SessionState{ID: "a"}}
	if se, err := s.FromArgs(nil); err != nil || se.ID != "a" {
		t.Errorf("no id: %q, %v", se.ID, err)
	}
	if se, err := s.FromArgs(registry.Args{"id": "a"}); err != nil || se.ID != "a" {
		t.Errorf("{id}: %q, %v", se.ID, err)
	}
	if _, err := s.FromArgs(registry.Args{"id": "nope"}); err == nil || err.Error() != `no session "nope"` {
		t.Errorf("an unknown id: %v", err)
	}
	if _, err := (testSessions{}).FromArgs(nil); err == nil || err.Error() != "no session is in front" {
		t.Errorf("none in front: %v", err)
	}
}
