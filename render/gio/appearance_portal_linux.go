package gio

import (
	"github.com/godbus/dbus/v5"
)

const (
	portalPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalSettings = "org.freedesktop.portal.Settings"
)

// appearanceSignals calls changed when the desktop portal says the light or dark preference changed, so it's read at
// once rather than at the next poll. Without a session bus or portal, the poll is all there is.
func appearanceSignals(changed func()) (stop func()) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return func() {}
	}
	end, err := watchPortal(conn, changed)
	if err != nil {
		conn.Close()
		return func() {}
	}
	return func() { end(); conn.Close() }
}

// watchPortal calls changed on each SettingChanged of org.freedesktop.appearance's color-scheme on conn.
func watchPortal(conn *dbus.Conn, changed func()) (stop func(), err error) {
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(portalPath), dbus.WithMatchInterface(portalSettings),
		dbus.WithMatchMember("SettingChanged"), dbus.WithMatchArg(0, "org.freedesktop.appearance")}
	if err := conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 8)
	conn.Signal(ch)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case s, ok := <-ch:
				if !ok {
					return
				}
				if s.Name == portalSettings+".SettingChanged" && len(s.Body) >= 2 && s.Body[0] == "org.freedesktop.appearance" &&
					s.Body[1] == "color-scheme" {
					changed()
				}
			}
		}
	}()
	return func() {
		conn.RemoveSignal(ch)
		conn.RemoveMatchSignal(match...)
		close(done)
	}, nil
}
