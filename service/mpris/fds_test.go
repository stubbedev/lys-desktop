package mpris

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-desktop/internal/fdtest"
)

func changedWith(iface string, fd dbus.UnixFD) dbusx.ChangedProperties {
	return dbusx.ChangedProperties{Interface: iface, Changed: map[string]dbus.Variant{"Handle": dbus.MakeVariant(fd)}, Invalidated: []string{}}
}

func emitChanged(t *testing.T, conn *dbus.Conn, c dbusx.ChangedProperties) {
	t.Helper()
	if err := dbusx.PropertiesChanged.Emit(conn, ObjectPath, c); err != nil {
		t.Fatal(err)
	}
}

func TestTheDescriptorsOfEverySignalTheServiceReadsAreClosed(t *testing.T) {
	bus := dbustest.Start(t)
	vlc := startFakePlayer(t, bus, BusNamePrefix+"vlc", "First", "Paused")
	svc, err := New(bus.Conn(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	pipe := fdtest.New(t)

	stranger := bus.Conn(t)
	emitChanged(t, stranger, changedWith(PlayerIface, pipe.Write))
	if err := stranger.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatal(err)
	}
	emitChanged(t, vlc.conn, changedWith("org.example.Other", pipe.Write))
	emitChanged(t, vlc.conn, changedWith(PlayerIface, pipe.Write))
	vlc.props.SetMust(PlayerIface, "PlaybackStatus", "Playing")
	waitFor(t, "the change behind the descriptors", func() bool {
		p, _ := svc.Active()
		return p.State == StatePlaying
	})
	pipe.RequireEveryCopyClosed(t)
}

type stalledPlayer struct {
	entered chan struct{}
	release chan struct{}
}

func (p stalledPlayer) GetAll(string) (map[string]dbus.Variant, *dbus.Error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return nil, dbusx.Failed("stalled")
}

var markerTick = dbusx.NewSignal[struct{}]("org.example.Marker", "Tick")

func TestCloseClosesTheDescriptorsOfTheSignalsItNeverRead(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	svc, err := New(conn, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stalled := stalledPlayer{entered: make(chan struct{}, 1), release: make(chan struct{})}
	slow := bus.Conn(t)
	if err := slow.Export(stalled, ObjectPath, dbusx.PropertiesInterface); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(stalled.release) })
	if reply, err := slow.RequestName(BusNamePrefix+"stalled", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request the stalled player's name: %v %v", reply, err)
	}
	select {
	case <-stalled.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the service never read the stalled player")
	}

	const markerName = "org.example.Marker"
	stranger := bus.Conn(t)
	if reply, err := stranger.RequestName(markerName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request %s: %v %v", markerName, reply, err)
	}
	ticked := make(chan struct{}, 1)
	marker, err := dbusx.WatchSignals(t.Context(), conn, dbusx.FromOwner(markerName), func(err error) { t.Errorf("the marker watch failed: %v", err) },
		dbusx.On(markerTick, "/", func(struct{}, dbus.Sequence) error {
			ticked <- struct{}{}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	pipe := fdtest.New(t)
	for range 8 {
		emitChanged(t, stranger, changedWith(PlayerIface, pipe.Write))
	}
	if err := markerTick.Emit(stranger, "/", struct{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("no Tick behind the descriptors, by which the connection has handed the service all of them")
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := marker.Stop(); err != nil {
		t.Fatalf("stop the marker watch: %v", err)
	}
	pipe.RequireEveryCopyClosed(t)
}
