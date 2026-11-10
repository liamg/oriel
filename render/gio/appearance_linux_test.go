package gio

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The desktop portal's SettingChanged for org.freedesktop.appearance color-scheme reads the preference again at
// once (xdg-desktop-portal, org.freedesktop.portal.Settings: "SettingChanged (namespace, key, value)"); another
// setting doesn't.
func TestPortalSettingChangedReadsAppearance(t *testing.T) {
	addr := startDBusDaemon(t)
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	changed := make(chan struct{}, 4)
	stop, err := watchPortal(conn, func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	portal, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer portal.Close()
	emit := func(ns, key string) {
		if err := portal.Emit(portalPath, portalSettings+".SettingChanged", ns, key, dbus.MakeVariant(uint32(1))); err != nil {
			t.Fatal(err)
		}
	}
	emit("org.freedesktop.appearance", "color-scheme")
	select {
	case <-changed:
	case <-time.After(30 * time.Second):
		t.Fatal("the change wasn't noticed")
	}
	signal := func(ns, key string) *dbus.Signal {
		return &dbus.Signal{Name: portalSettings + ".SettingChanged", Body: []any{ns, key, dbus.MakeVariant(uint32(1))}}
	}
	for _, s := range []*dbus.Signal{signal("org.gnome.desktop.interface", "font-name"), signal("org.freedesktop.appearance", "contrast")} {
		if colorSchemeChanged(s) {
			t.Errorf("%v's change was taken for the color scheme's", s.Body[:2])
		}
	}
}
