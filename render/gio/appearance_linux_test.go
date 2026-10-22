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
	emit("org.gnome.desktop.interface", "font-name")
	emit("org.freedesktop.appearance", "color-scheme")
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("the change wasn't noticed")
	}
	select {
	case <-changed:
		t.Error("another setting's change was taken for the appearance's")
	case <-time.After(50 * time.Millisecond):
	}
}
