// Package bussignal is the upkeep of a signal channel a reader holds
// itself, because its signals come from peers or paths no dbusx watch
// can name ahead of time. Every channel owns the descriptors of the
// signals it is handed, so such a reader closes those of each signal it
// does not take over (dbus.Signal.CloseFDs) and takes its channel off
// the connection with Remove.
package bussignal

import (
	"errors"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Remove takes signals off conn and closes the descriptors of the
// signals it was sent and nobody read. Nothing is sent on signals once
// it returns.
func Remove(conn *dbus.Conn, signals chan *dbus.Signal) error {
	conn.RemoveSignal(signals)
	var errs []error
	for {
		select {
		case sig, open := <-signals:
			if !open {
				return errors.Join(errs...)
			}
			if err := sig.CloseFDs(); err != nil {
				errs = append(errs, fmt.Errorf("discard %s from %s: %w", sig.Name, sig.Sender, err))
			}
		default:
			return errors.Join(errs...)
		}
	}
}
