// Package mpris is the media player service over MPRIS2 (D-Bus), the
// Go counterpart of crates/wayle-media: it discovers players by bus
// name, keeps each one's snapshot current from PropertiesChanged,
// picks the active player the way selection.rs does, and drives the
// transport (play/pause, next, previous, seek, loop, shuffle).
package mpris

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-desktop/feed"
	"github.com/stubbedev/lys-desktop/glob"
)

// D-Bus names of the MPRIS2 surface.
const (
	BusNamePrefix = playerNames + "."
	ObjectPath    = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	RootIface     = "org.mpris.MediaPlayer2"
	PlayerIface   = "org.mpris.MediaPlayer2.Player"
	// noTrack is the MPRIS "no track" object path SetPosition takes when
	// the player publishes no mpris:trackid (player/mod.rs NULL_PATH).
	noTrack = dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")

	playerNames = "org.mpris.MediaPlayer2"
)

// ErrNoPlayer reports a control aimed at a bus name the service does
// not track.
var ErrNoPlayer = errors.New("mpris: no such player")

// PlaybackState is the player's transport state.
type PlaybackState uint8

// Playback states.
const (
	StateStopped PlaybackState = iota
	StatePlaying
	StatePaused
)

// StateFromString maps MPRIS's PlaybackStatus.
func StateFromString(status string) PlaybackState {
	switch strings.TrimSpace(status) {
	case "Playing":
		return StatePlaying
	case "Paused":
		return StatePaused
	}
	return StateStopped
}

// LoopMode is the player's LoopStatus; LoopUnsupported marks a player
// without the property or with an unknown value (types.rs LoopMode).
type LoopMode uint8

// Loop modes.
const (
	LoopNone LoopMode = iota
	LoopTrack
	LoopPlaylist
	LoopUnsupported
)

// ParseLoopMode maps MPRIS's LoopStatus.
func ParseLoopMode(status string) LoopMode {
	switch status {
	case "None":
		return LoopNone
	case "Track":
		return LoopTrack
	case "Playlist":
		return LoopPlaylist
	}
	return LoopUnsupported
}

// wire is the LoopStatus spelling; unsupported has none.
func (m LoopMode) wire() (string, bool) {
	switch m {
	case LoopNone:
		return "None", true
	case LoopTrack:
		return "Track", true
	case LoopPlaylist:
		return "Playlist", true
	}
	return "", false
}

// Next is toggle_loop's cycle: None, Track, Playlist, None; false for
// an unsupported mode.
func (m LoopMode) Next() (LoopMode, bool) {
	switch m {
	case LoopNone:
		return LoopTrack, true
	case LoopTrack:
		return LoopPlaylist, true
	case LoopPlaylist:
		return LoopNone, true
	}
	return LoopUnsupported, false
}

const playerCallTimeout = 5 * time.Second

// Player is one player's snapshot.
type Player struct {
	// BusName is the full well-known name (org.mpris.MediaPlayer2.x).
	BusName      string
	Identity     string
	DesktopEntry string

	Title   string
	Artist  string
	Album   string
	ArtURL  string
	TrackID string
	// Length is the track length; zero when the player reports none.
	Length time.Duration

	State   PlaybackState
	Loop    LoopMode
	Shuffle bool

	CanGoNext     bool
	CanGoPrevious bool
	CanSeek       bool
	// CanLoop and CanShuffle report whether the player publishes the
	// LoopStatus and Shuffle properties at all (player/mod.rs probes
	// the reads the same way).
	CanLoop    bool
	CanShuffle bool
	// Volume is the player's Volume property, 0..1 (0 when unpublished).
	Volume float64
}

// SelectBest is selection.rs select_best_player: the first priority
// pattern (in order) that matches a player's bus name, else the first
// playing player, else the first player. Patterns are wayle-media's
// case-sensitive wildcards.
func SelectBest(players []Player, priority []string) (Player, bool) {
	for _, pattern := range priority {
		for _, p := range players {
			if glob.Wildcard(pattern, p.BusName) {
				return p, true
			}
		}
	}
	for _, p := range players {
		if p.State == StatePlaying {
			return p, true
		}
	}
	if len(players) > 0 {
		return players[0], true
	}
	return Player{}, false
}

// Ignored is monitoring.rs should_ignore: a plain substring match of
// any pattern against the bus name.
func Ignored(busName string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(busName, pattern) {
			return true
		}
	}
	return false
}

// Source is what the media module and dropdown read and drive.
type Source interface {
	// Players lists the tracked players in discovery order.
	Players() []Player
	// Active returns the active player; false when there is none.
	Active() (Player, bool)
	// SetActive makes the tracked player with that bus name active (the
	// source picker); unknown names are ErrNoPlayer.
	SetActive(busName string) error
	// Subscribe returns a feed that ticks on every player-set, active,
	// or snapshot change; stop ends it.
	Subscribe() (<-chan struct{}, func())
	// PlayPause, Next, and Previous drive the transport.
	PlayPause(ctx context.Context, busName string) error
	Next(ctx context.Context, busName string) error
	Previous(ctx context.Context, busName string) error
	// SetPosition seeks the current track to pos.
	SetPosition(ctx context.Context, busName string, pos time.Duration) error
	// ToggleLoop and ToggleShuffle step the loop cycle and flip shuffle.
	ToggleLoop(ctx context.Context, busName string) error
	ToggleShuffle(ctx context.Context, busName string) error
	// Position reads the live playback position (MPRIS emits no change
	// signal for it, so the dropdown polls).
	Position(ctx context.Context, busName string) (time.Duration, error)
}

// Service is the live Source on a bus.
type Service struct {
	conn     *dbus.Conn
	ignored  []string
	priority []string

	membership sync.Mutex
	names      *dbusx.Watch

	mu      sync.Mutex
	players []Player
	watches map[string]*dbusx.Watch
	active  string
	ticks   *feed.Tick

	ctx    context.Context
	cancel context.CancelFunc

	now         func() time.Time
	requestedAt map[string]time.Time
	tracks      *feed.Feed[TrackChange]
}

// TrackChange is the active player moving to another track. Requested
// marks a change the user asked for through the daemon's Next or
// Previous (a media key, `lys-shell media next`), not the player
// advancing on its own.
type TrackChange struct {
	Player    Player
	Requested bool
}

const requestWindow = 3 * time.Second

func (p Player) sameTrack(q Player) bool {
	return p.TrackID == q.TrackID && p.Title == q.Title && p.Artist == q.Artist && p.Album == q.Album
}

const messageBus = "org.freedesktop.DBus"

// New starts the service on conn, which stays the caller's: it watches
// the bus announce player names changing owner, discovers the players
// already on the bus, and follows each one's PropertiesChanged until
// Close. ignored and priority are the players-ignored and
// player-priority config lists.
func New(conn *dbus.Conn, ignored, priority []string) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		conn:        conn,
		ignored:     ignored,
		priority:    priority,
		watches:     make(map[string]*dbusx.Watch),
		ticks:       feed.NewTick(),
		ctx:         ctx,
		cancel:      cancel,
		now:         time.Now,
		requestedAt: make(map[string]time.Time),
		tracks:      feed.New[TrackChange](4),
	}
	s.membership.Lock()
	names, err := dbusx.WatchSignals(ctx, conn, dbusx.FromBus(), logFailure,
		dbusx.OnNameOwnerChanged(s.ownerChanged).WithArg0Namespace(playerNames))
	if err != nil {
		s.membership.Unlock()
		cancel()
		return nil, fmt.Errorf("mpris: watch player names: %w", err)
	}
	s.names = names
	err = s.discover()
	s.membership.Unlock()
	if err != nil {
		cancel()
		return nil, errors.Join(err, names.Stop())
	}
	return s, nil
}

func (s *Service) discover() error {
	listing, cancel := context.WithTimeout(s.ctx, playerCallTimeout)
	defer cancel()
	var listed []string
	if err := s.conn.BusObject().CallWithContext(listing, messageBus+".ListNames", 0).Store(&listed); err != nil {
		return fmt.Errorf("mpris: list names: %w", err)
	}
	for _, name := range listed {
		if strings.HasPrefix(name, BusNamePrefix) && !Ignored(name, s.ignored) {
			if err := s.add(name); err != nil {
				log.Printf("mpris: %v", err)
			}
		}
	}
	return nil
}

func logFailure(err error) { log.Printf("mpris: %v", err) }

// Close stops following the bus and returns once every watch of the
// service has ended; the connection stays open.
func (s *Service) Close() error {
	s.cancel()
	errs := []error{s.names.Stop()}
	s.mu.Lock()
	watches := slices.Collect(maps.Values(s.watches))
	clear(s.watches)
	s.mu.Unlock()
	for _, w := range watches {
		errs = append(errs, w.Stop())
	}
	s.ticks.Close()
	s.tracks.Close()
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("mpris: close: %w", err)
	}
	return nil
}

// ownerChanged adds, removes, or replaces a player as its bus name
// changes owner (monitoring.rs spawn_name_monitoring).
func (s *Service) ownerChanged(change dbusx.OwnerChange, _ dbus.Sequence) error {
	if !strings.HasPrefix(change.Name, BusNamePrefix) {
		return nil
	}
	s.membership.Lock()
	defer s.membership.Unlock()
	if change.NewOwner == "" || Ignored(change.Name, s.ignored) {
		return s.remove(change.Name)
	}
	return s.add(change.Name)
}

// playerRoutes re-read the player name's snapshot on a PropertiesChanged
// of its root or player interface, once ready is closed.
func (s *Service) playerRoutes(name string, ready <-chan struct{}) []dbusx.Route {
	refresh := func(dbusx.ChangedProperties, dbus.Sequence) error {
		<-ready
		return s.refresh(name)
	}
	return []dbusx.Route{
		dbusx.On(dbusx.PropertiesChanged, ObjectPath, refresh).WithArg0(PlayerIface),
		dbusx.On(dbusx.PropertiesChanged, ObjectPath, refresh).WithArg0(RootIface),
	}
}

// add reads a player, replacing one tracked under its name, and
// re-selects the active one, as handle_player_added does on every
// arrival. Its PropertiesChanged are watched before it is read and
// handled only once the read is stored, so none sent after the read
// began is lost or overtaken by the read. The caller holds membership.
func (s *Service) add(name string) error {
	if err := s.remove(name); err != nil {
		return err
	}
	ready := make(chan struct{})
	w, err := dbusx.WatchSignals(s.ctx, s.conn, dbusx.FromOwner(name), logFailure, s.playerRoutes(name, ready)...)
	if err != nil {
		return fmt.Errorf("follow new player %s: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, playerCallTimeout)
	defer cancel()
	p, err := s.read(ctx, name)
	if err == nil {
		s.track(p, w)
	}
	close(ready)
	if err != nil {
		return errors.Join(fmt.Errorf("read new player: %w", err), w.Stop())
	}
	s.notify()
	return nil
}

func (s *Service) track(p Player, w *dbusx.Watch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.players = append(s.players, p)
	s.watches[p.BusName] = w
	if best, ok := SelectBest(s.players, s.priority); ok {
		s.active = best.BusName
	}
}

// remove drops a player and stops following it; the active one leaving
// re-selects (handle_player_removed). The caller holds membership.
func (s *Service) remove(name string) error {
	s.mu.Lock()
	w := s.watches[name]
	delete(s.watches, name)
	i := slices.IndexFunc(s.players, func(p Player) bool { return p.BusName == name })
	if i >= 0 {
		s.players = slices.Delete(s.players, i, i+1)
		if s.active == name {
			s.active = ""
			if best, ok := SelectBest(s.players, s.priority); ok {
				s.active = best.BusName
			}
		}
	}
	s.mu.Unlock()
	if i >= 0 {
		s.notify()
	}
	if w == nil {
		return nil
	}
	if err := w.Stop(); err != nil {
		return fmt.Errorf("stop following %s: %w", name, err)
	}
	return nil
}

// store replaces the player's snapshot, reporting whether that changed
// it, and the track change it is when the active player moved to
// another titled track.
func (s *Service) store(p Player) (bool, TrackChange, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.players {
		if s.players[i].BusName != p.BusName {
			continue
		}
		prev := s.players[i]
		s.players[i] = p
		if prev.sameTrack(p) || p.Title == "" || p.BusName != s.active {
			return prev != p, TrackChange{}, false
		}
		deadline, asked := s.requestedAt[p.BusName]
		delete(s.requestedAt, p.BusName)
		return true, TrackChange{Player: p, Requested: asked && !s.now().After(deadline)}, true
	}
	return false, TrackChange{}, false
}

// requested wraps a transport call as one the user asked for: the
// player's next track change within requestWindow is Requested.
func (s *Service) requested(run func(context.Context, string) error) func(context.Context, string) error {
	return func(ctx context.Context, busName string) error {
		s.mu.Lock()
		s.requestedAt[busName] = s.now().Add(requestWindow)
		s.mu.Unlock()
		err := run(ctx, busName)
		if err != nil {
			s.mu.Lock()
			delete(s.requestedAt, busName)
			s.mu.Unlock()
		}
		return err
	}
}

// TrackChanges returns a feed of the active player's track changes;
// stop ends it.
func (s *Service) TrackChanges() (<-chan TrackChange, func()) { return s.tracks.Subscribe() }

// refresh re-reads one player's snapshot in place.
func (s *Service) refresh(name string) error {
	ctx, cancel := context.WithTimeout(s.ctx, playerCallTimeout)
	defer cancel()
	p, err := s.read(ctx, name)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	changed, change, moved := s.store(p)
	if moved {
		s.tracks.Publish(change)
	}
	if changed {
		s.notify()
	}
	return nil
}

// read pulls one player's root and player properties.
func (s *Service) read(ctx context.Context, name string) (Player, error) {
	obj := s.conn.Object(name, ObjectPath)
	var root, player map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, dbusx.PropertiesInterface+".GetAll", 0, RootIface).Store(&root); err != nil {
		return Player{}, fmt.Errorf("%s root: %w", name, err)
	}
	if err := obj.CallWithContext(ctx, dbusx.PropertiesInterface+".GetAll", 0, PlayerIface).Store(&player); err != nil {
		return Player{}, fmt.Errorf("%s player: %w", name, err)
	}
	return parsePlayer(name, root, player), nil
}

// parsePlayer builds a snapshot from the two GetAll maps.
func parsePlayer(name string, root, player map[string]dbus.Variant) Player {
	p := Player{
		BusName:       name,
		Identity:      variantString(root["Identity"]),
		DesktopEntry:  variantString(root["DesktopEntry"]),
		State:         StateFromString(variantString(player["PlaybackStatus"])),
		CanGoNext:     variantBool(player["CanGoNext"]),
		CanGoPrevious: variantBool(player["CanGoPrevious"]),
		CanSeek:       variantBool(player["CanSeek"]),
		Loop:          LoopUnsupported,
	}
	p.Volume, _ = player["Volume"].Value().(float64)
	if loop, ok := player["LoopStatus"]; ok {
		p.CanLoop = true
		p.Loop = ParseLoopMode(variantString(loop))
	}
	if shuffle, ok := player["Shuffle"]; ok {
		p.CanShuffle = true
		p.Shuffle = variantBool(shuffle)
	}
	var meta map[string]dbus.Variant
	if v, ok := player["Metadata"]; ok {
		meta, _ = v.Value().(map[string]dbus.Variant)
	}
	p.Title = variantString(meta["xesam:title"])
	p.Artist = strings.Join(variantStringArray(meta["xesam:artist"]), ", ")
	p.Album = variantString(meta["xesam:album"])
	p.ArtURL = variantString(meta["mpris:artUrl"])
	p.TrackID = variantString(meta["mpris:trackid"])
	p.Length = variantMicros(meta["mpris:length"])
	return p
}

// notify ticks every subscriber, coalescing per feed.
func (s *Service) notify() { feed.Notify(s.ticks) }

// Subscribe implements Source.
func (s *Service) Subscribe() (<-chan struct{}, func()) { return s.ticks.Subscribe() }

// Players implements Source.
func (s *Service) Players() []Player {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Player, len(s.players))
	copy(out, s.players)
	return out
}

// Active implements Source.
func (s *Service) Active() (Player, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.players {
		if p.BusName == s.active {
			return p, true
		}
	}
	return Player{}, false
}

// SetActive implements Source (service.rs set_active_player).
// An empty bus name clears the active player.
func (s *Service) SetActive(busName string) error {
	s.mu.Lock()
	if busName == "" {
		s.active = ""
		s.mu.Unlock()
		s.notify()
		return nil
	}
	found := false
	for _, p := range s.players {
		if p.BusName == busName {
			found = true
		}
	}
	if found {
		s.active = busName
	}
	s.mu.Unlock()
	if !found {
		return fmt.Errorf("%w: %s", ErrNoPlayer, busName)
	}
	s.notify()
	return nil
}

// lookup returns the tracked snapshot for a control.
func (s *Service) lookup(busName string) (Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.players {
		if p.BusName == busName {
			return p, nil
		}
	}
	return Player{}, fmt.Errorf("%w: %s", ErrNoPlayer, busName)
}

// call runs one Player-interface method on a tracked player.
func (s *Service) call(ctx context.Context, busName, method string, args ...any) error {
	if _, err := s.lookup(busName); err != nil {
		return err
	}
	return s.conn.Object(busName, ObjectPath).CallWithContext(ctx, PlayerIface+"."+method, 0, args...).Err
}

// setProp writes one Player-interface property on a tracked player.
func (s *Service) setProp(ctx context.Context, busName, prop string, value any) error {
	return s.conn.Object(busName, ObjectPath).CallWithContext(ctx, dbusx.PropertiesInterface+".Set", 0, PlayerIface, prop, dbus.MakeVariant(value)).Err
}

// PlayPause implements Source.
func (s *Service) PlayPause(ctx context.Context, busName string) error {
	return s.call(ctx, busName, "PlayPause")
}

// Next implements Source.
func (s *Service) Next(ctx context.Context, busName string) error {
	return s.call(ctx, busName, "Next")
}

// Previous implements Source.
func (s *Service) Previous(ctx context.Context, busName string) error {
	return s.call(ctx, busName, "Previous")
}

// SetPosition implements Source: SetPosition on the current track id,
// the NoTrack path when the player publishes none.
func (s *Service) SetPosition(ctx context.Context, busName string, pos time.Duration) error {
	p, err := s.lookup(busName)
	if err != nil {
		return err
	}
	track := noTrack
	if p.TrackID != "" && dbus.ObjectPath(p.TrackID).IsValid() {
		track = dbus.ObjectPath(p.TrackID)
	}
	return s.call(ctx, busName, "SetPosition", track, pos.Microseconds())
}

// ToggleLoop implements Source: the None, Track, Playlist cycle.
func (s *Service) ToggleLoop(ctx context.Context, busName string) error {
	p, err := s.lookup(busName)
	if err != nil {
		return err
	}
	next, ok := p.Loop.Next()
	if !ok || !p.CanLoop {
		return fmt.Errorf("mpris: %s: loop unsupported", busName)
	}
	wire, _ := next.wire()
	return s.setProp(ctx, busName, "LoopStatus", wire)
}

// ToggleShuffle implements Source.
func (s *Service) ToggleShuffle(ctx context.Context, busName string) error {
	p, err := s.lookup(busName)
	if err != nil {
		return err
	}
	if !p.CanShuffle {
		return fmt.Errorf("mpris: %s: shuffle unsupported", busName)
	}
	return s.setProp(ctx, busName, "Shuffle", !p.Shuffle)
}

// SetShuffle sets the Shuffle property (player set_shuffle_mode).
func (s *Service) SetShuffle(ctx context.Context, busName string, on bool) error {
	if _, err := s.lookup(busName); err != nil {
		return err
	}
	return s.setProp(ctx, busName, "Shuffle", on)
}

// SetLoop sets the LoopStatus property (player set_loop_mode);
// LoopUnsupported is refused.
func (s *Service) SetLoop(ctx context.Context, busName string, mode LoopMode) error {
	wire, ok := mode.wire()
	if !ok {
		return fmt.Errorf("mpris: %s: loop mode %d has no LoopStatus", busName, mode)
	}
	if _, err := s.lookup(busName); err != nil {
		return err
	}
	return s.setProp(ctx, busName, "LoopStatus", wire)
}

// Position implements Source.
func (s *Service) Position(ctx context.Context, busName string) (time.Duration, error) {
	if _, err := s.lookup(busName); err != nil {
		return 0, err
	}
	var v dbus.Variant
	if err := s.conn.Object(busName, ObjectPath).CallWithContext(ctx, dbusx.PropertiesInterface+".Get", 0, PlayerIface, "Position").Store(&v); err != nil {
		return 0, err
	}
	return variantMicros(v), nil
}

func variantString(v dbus.Variant) string {
	switch value := v.Value().(type) {
	case string:
		return value
	case dbus.ObjectPath:
		return string(value)
	}
	return ""
}

func variantStringArray(v dbus.Variant) []string {
	switch value := v.Value().(type) {
	case []string:
		return value
	case string:
		return []string{value}
	}
	return nil
}

func variantBool(v dbus.Variant) bool {
	b, _ := v.Value().(bool)
	return b
}

// variantMicros reads an MPRIS microsecond count (x or t); zero for
// anything non-positive or absent (metadata types.rs duration).
func variantMicros(v dbus.Variant) time.Duration {
	switch value := v.Value().(type) {
	case int64:
		if value > 0 {
			return intconv.Duration(value, time.Microsecond)
		}
	case uint64:
		if value > 0 {
			return intconv.Duration(value, time.Microsecond)
		}
	case int32:
		if value > 0 {
			return time.Duration(value) * time.Microsecond
		}
	}
	return 0
}
