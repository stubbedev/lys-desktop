package upower_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-desktop/internal/fdtest"
	"github.com/stubbedev/lys-desktop/service/upower"
)

func TestSubscriptionsCloseTheDescriptorsOfEverySignalTheyRead(t *testing.T) {
	bus := dbustest.Start(t)
	sys := upower.NewOn(bus.Conn(t))
	display, stopDisplay, err := sys.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	peripherals, stopPeripherals, err := sys.SubscribePeripherals(t.Context())
	if err != nil {
		t.Fatal(errors.Join(err, stopDisplay()))
	}
	pipe := fdtest.New(t)
	sender := bus.Conn(t)
	for _, path := range []dbus.ObjectPath{upower.DisplayDevicePath, mousePath} {
		err := sender.Emit(path, "org.freedesktop.DBus.Properties.PropertiesChanged", "org.freedesktop.UPower.Device",
			map[string]dbus.Variant{"Handle": dbus.MakeVariant(pipe.Write)}, []string{})
		if err != nil {
			t.Fatal(err)
		}
	}
	for what, ticks := range map[string]<-chan struct{}{"the DisplayDevice": display, "a peripheral": peripherals} {
		select {
		case <-ticks:
		case <-time.After(5 * time.Second):
			t.Fatalf("no tick for %s's PropertiesChanged", what)
		}
	}
	requireStopped(t, "the battery", stopDisplay)
	requireStopped(t, "the peripherals", stopPeripherals)
	for what, ticks := range map[string]<-chan struct{}{"battery": display, "peripherals": peripherals} {
		if _, open := <-ticks; open {
			t.Errorf("the %s feed is open after stop", what)
		}
	}
	pipe.RequireEveryCopyClosed(t)
}
