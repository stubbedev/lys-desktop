package mpris

import (
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type heldPlayer struct {
	mu      sync.Mutex
	status  string
	reads   int
	entered chan struct{}
	again   chan struct{}
	release chan struct{}
}

func (p *heldPlayer) setStatus(status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
}

func (p *heldPlayer) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != PlayerIface {
		return map[string]dbus.Variant{"Identity": dbus.MakeVariant("Held player")}, nil
	}
	p.mu.Lock()
	status := p.status
	p.reads++
	reads := p.reads
	p.mu.Unlock()
	switch reads {
	case 1:
		close(p.entered)
		<-p.release
	case 2:
		close(p.again)
	}
	return map[string]dbus.Variant{"PlaybackStatus": dbus.MakeVariant(status)}, nil
}

func TestAChangeSentWhileANewPlayerIsReadIsNotOvertakenByTheRead(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	svc, err := New(conn, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	held := &heldPlayer{status: "Paused", entered: make(chan struct{}), again: make(chan struct{}), release: make(chan struct{})}
	player := bus.Conn(t)
	if err := player.Export(held, ObjectPath, dbusx.PropertiesInterface); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			close(held.release)
		}
	}
	t.Cleanup(release)
	const markerName = "org.example.Marker"
	for _, name := range []string{BusNamePrefix + "held", markerName} {
		if reply, err := player.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("request %s: %v %v", name, reply, err)
		}
	}
	select {
	case <-held.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the service never read the new player")
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
	t.Cleanup(func() { _ = marker.Stop() })
	held.setStatus("Playing")
	change := dbusx.ChangedProperties{Interface: PlayerIface, Changed: map[string]dbus.Variant{"PlaybackStatus": dbus.MakeVariant("Playing")}, Invalidated: []string{}}
	if err := dbusx.PropertiesChanged.Emit(player, ObjectPath, change); err != nil {
		t.Fatal(err)
	}
	if err := markerTick.Emit(player, "/", struct{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("no Tick behind the change, by which the service's connection has read it")
	}
	select {
	case <-held.again:
	case <-time.After(200 * time.Millisecond):
	}
	release()

	waitFor(t, "the change sent during the read", func() bool {
		p, ok := svc.Active()
		return ok && p.State == StatePlaying
	})
}
