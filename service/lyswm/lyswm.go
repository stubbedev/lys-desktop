// Package lyswm is the lys-wm IPC client: one JSON request line per
// connection on $LYS_WM_SOCKET, answered by one response line, and the
// watch stream of event lines (lys-wm's docs/ipc.md).
package lyswm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/wmipc"

	"github.com/stubbedev/lys-desktop/ipcsock"
)

// ErrNotRunning is a request made while no lys-wm socket is advertised.
var ErrNotRunning = errors.New("lys-wm: " + wmipc.SocketEnv + " is not set")

// SocketPath reads the advertised socket; empty when lys-wm is not
// running.
func SocketPath() string { return os.Getenv(wmipc.SocketEnv) }

// IsRunning reports whether a lys-wm socket is advertised.
func IsRunning() bool { return SocketPath() != "" }

// RejectedError is a request lys-wm answered with an error.
type RejectedError struct {
	Request string
	Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("lys-wm rejected %s: %s", e.Request, e.Message)
}

type request struct {
	Request string      `json:"request"`
	Action  string      `json:"action,omitempty"`
	Name    string      `json:"name,omitempty"`
	Mode    *wmipc.Mode `json:"mode,omitempty"`
	Scale   float64     `json:"scale,omitempty"`
}

func open(req request) (net.Conn, *bufio.Reader, error) {
	path := SocketPath()
	if path == "" {
		return nil, nil, ErrNotRunning
	}
	conn, err := ipcsock.Standard.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("lys-wm: %s: %w", req.Request, err)
	}
	line, err := json.Marshal(req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("lys-wm: encode %s: %w", req.Request, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("lys-wm: send %s: %w", req.Request, err)
	}
	return conn, bufio.NewReader(conn), nil
}

func readResponse(reader *bufio.Reader, req request) (wmipc.Response, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return wmipc.Response{}, fmt.Errorf("lys-wm: read the %s answer: %w", req.Request, err)
	}
	var resp wmipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return wmipc.Response{}, fmt.Errorf("lys-wm: parse the %s answer %q: %w", req.Request, line, err)
	}
	if resp.Version != wmipc.Version {
		return wmipc.Response{}, fmt.Errorf("lys-wm: %s answered IPC version %d, this client speaks %d", req.Request, resp.Version, wmipc.Version)
	}
	if resp.Error != "" {
		return wmipc.Response{}, &RejectedError{Request: req.Request, Message: resp.Error}
	}
	return resp, nil
}

func call(req request, out any) error {
	conn, reader, err := open(req)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	resp, err := readResponse(reader, req)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("lys-wm: parse the %s result: %w", req.Request, err)
	}
	return nil
}

// Outputs lists the outputs.
func Outputs() ([]wmipc.Output, error) {
	var out []wmipc.Output
	return out, call(request{Request: "outputs"}, &out)
}

// Workspaces lists the open workspaces in ID order.
func Workspaces() ([]wmipc.Workspace, error) {
	var out []wmipc.Workspace
	return out, call(request{Request: "workspaces"}, &out)
}

// Windows lists the mapped windows, oldest first.
func Windows() ([]wmipc.Window, error) {
	var out []wmipc.Window
	return out, call(request{Request: "windows"}, &out)
}

// Stacked is the windows an output shows, front to back on each: those
// with a stack index, by it.
func Stacked(windows []wmipc.Window) []wmipc.Window {
	shown := slices.DeleteFunc(slices.Clone(windows), func(w wmipc.Window) bool { return w.Stack == nil })
	slices.SortStableFunc(shown, func(a, b wmipc.Window) int { return *a.Stack - *b.Stack })
	return shown
}

// ToplevelIdentifier is the ext-foreign-toplevel-list identifier lys-wm
// gives the window with IPC id id.
func ToplevelIdentifier(id uint64) string { return "lys-wm-" + strconv.FormatUint(id, 10) }

// ActiveWindow is the focused window; false when none has focus.
func ActiveWindow() (wmipc.Window, bool, error) {
	var out *wmipc.Window
	if err := call(request{Request: "activewindow"}, &out); err != nil {
		return wmipc.Window{}, false, err
	}
	if out == nil {
		return wmipc.Window{}, false, nil
	}
	return *out, true, nil
}

func result[T any](name string) (T, error) {
	var out *T
	if err := call(request{Request: name}, &out); err != nil {
		var zero T
		return zero, err
	}
	if out == nil {
		var zero T
		return zero, fmt.Errorf("lys-wm: %s answered no result", name)
	}
	return *out, nil
}

// Dispatch runs one action, as a bind would.
func Dispatch(action string) error {
	return call(request{Request: "dispatch", Action: action}, nil)
}

type event struct {
	Event     wmipc.EventKind `json:"event"`
	Submap    *string         `json:"submap"`
	Layout    *wmipc.Layout   `json:"layout"`
	Locks     *wmipc.Locks    `json:"locks"`
	Motion    *bool           `json:"reduced_motion"`
	Window    json.RawMessage `json:"window"`
	Workspace *int            `json:"workspace"`
	Title     *string         `json:"title"`
	AppID     *string         `json:"app_id"`
	Output    json.RawMessage `json:"output"`
	Rect      *wmipc.Rect     `json:"rect"`
	Urgent    *bool           `json:"urgent"`
	Minimized *bool           `json:"minimized"`
	Stack     json.RawMessage `json:"stack"`
}

// Watcher follows the watch stream and ticks after each event of the
// kinds it was asked for.
type Watcher struct {
	conn       net.Conn
	kinds      []wmipc.EventKind
	onEvent    func(event)
	hidesTitle func(window uint64) bool
	beforeTick func()
	ticks      chan struct{}
	once       sync.Once
}

// Watch opens the event stream. It ticks on the given kinds, or on
// every event when none is given; Ticks closes when the stream ends.
func Watch(kinds ...wmipc.EventKind) (*Watcher, error) {
	return watch(kinds, nil, nil, nil, nil)
}

// WatchTitles is Watch on the given kinds and the title events, but for those
// of the windows hidden reports; before runs ahead of every tick, while the
// event that caused it has not been read back yet.
func WatchTitles(hidden func(window uint64) bool, before func(), kinds ...wmipc.EventKind) (*Watcher, error) {
	return watch(append(slices.Clone(kinds), wmipc.EventTitle), nil, nil, hidden, before)
}

func watch(kinds []wmipc.EventKind, seed func() error, onEvent func(event), hidesTitle func(uint64) bool, beforeTick func()) (*Watcher, error) {
	req := request{Request: "watch"}
	conn, reader, err := open(req)
	if err != nil {
		return nil, err
	}
	if _, err := readResponse(reader, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ipcsock.Disarm(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: watch: %w", err)
	}
	if seed != nil {
		if err := seed(); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("lys-wm: watch: %w", err)
		}
	}
	w := &Watcher{conn: conn, kinds: kinds, onEvent: onEvent, hidesTitle: hidesTitle, beforeTick: beforeTick, ticks: make(chan struct{}, 1)}
	go w.pump(reader)
	return w, nil
}

func (w *Watcher) pump(reader *bufio.Reader) {
	defer close(w.ticks)
	defer w.Close()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil || e.Event == "" {
			log.Printf("lys-wm: event stream ended on a line it cannot read (%v): %.200q", err, line) //nolint:gosec // %q escapes the line
			return
		}
		if w.onEvent != nil {
			w.onEvent(e)
		}
		if len(w.kinds) > 0 && !slices.Contains(w.kinds, e.Event) {
			continue
		}
		if w.dropsTitle(e) {
			continue
		}
		if w.beforeTick != nil {
			w.beforeTick()
		}
		select {
		case w.ticks <- struct{}{}:
		default:
		}
	}
}

func (w *Watcher) dropsTitle(e event) bool {
	if w.hidesTitle == nil || e.Event != wmipc.EventTitle {
		return false
	}
	var window uint64
	return json.Unmarshal(e.Window, &window) == nil && w.hidesTitle(window)
}

// State is one piece of lys-wm state a client can follow: the request
// that answers it and the event that reports its changes.
type State[T any] struct {
	kind    wmipc.EventKind
	payload func(event) *T
}

// The followable states.
var (
	SubmapState = State[string]{wmipc.EventSubmap, func(e event) *string { return e.Submap }}
	LayoutState = State[wmipc.Layout]{wmipc.EventLayout, func(e event) *wmipc.Layout { return e.Layout }}
	LocksState  = State[wmipc.Locks]{wmipc.EventLocks, func(e event) *wmipc.Locks { return e.Locks }}
	// ReducedMotionState is whether animations.reduced_motion is in force.
	ReducedMotionState = State[bool]{wmipc.EventReducedMotion, func(e event) *bool { return e.Motion }}
)

// FollowReducedMotion calls set with lys-wm's reduced motion now and at
// every change, and gone when the stream ends, by the returned stop or
// because lys-wm went away. It does nothing without a lys-wm session.
func FollowReducedMotion(set func(bool), gone func()) (stop func(), err error) {
	if !IsRunning() {
		return func() {}, nil
	}
	w, err := WatchState(ReducedMotionState)
	if err != nil {
		return nil, err
	}
	set(w.Value())
	go func() {
		for range w.Ticks() {
			set(w.Value())
		}
		gone()
	}()
	return w.Close, nil
}

// StateWatcher follows one State: seeded from its request once the
// stream is open, so an event sent in between wins, then updated by its
// event, and ticking after each one.
type StateWatcher[T any] struct {
	*Watcher
	mu    sync.Mutex
	value T
}

// WatchState opens the event stream for one State.
func WatchState[T any](s State[T]) (*StateWatcher[T], error) {
	sw := &StateWatcher[T]{}
	seed := func() error {
		v, err := result[T](string(s.kind))
		if err != nil {
			return fmt.Errorf("seed the %s: %w", s.kind, err)
		}
		sw.value = v
		return nil
	}
	onEvent := func(e event) {
		if e.Event != s.kind {
			return
		}
		if v := s.payload(e); v != nil {
			sw.mu.Lock()
			sw.value = *v
			sw.mu.Unlock()
		}
	}
	w, err := watch([]wmipc.EventKind{s.kind}, seed, onEvent, nil, nil)
	if err != nil {
		return nil, err
	}
	sw.Watcher = w
	return sw, nil
}

// Value is the state as last seeded or reported.
func (s *StateWatcher[T]) Value() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Ticks fires after each followed event; it closes when the stream
// ends.
func (w *Watcher) Ticks() <-chan struct{} { return w.ticks }

// Close ends the stream.
func (w *Watcher) Close() {
	w.once.Do(func() { _ = w.conn.Close() })
}

// createOutput is the request that adds a virtual output.
const createOutput = "createoutput"

// CreateOutput adds a virtual output of the mode, at the scale (0 leaves
// it to lys-wm's output rules), and returns its name.
func CreateOutput(mode wmipc.Mode, scale float64) (string, error) {
	var out struct {
		Name string `json:"name"`
	}
	if err := call(request{Request: createOutput, Mode: &mode, Scale: scale}, &out); err != nil {
		return "", err
	}
	if out.Name == "" {
		return "", fmt.Errorf("lys-wm: %s answered no output name", createOutput)
	}
	return out.Name, nil
}

// RemoveOutput removes the virtual output name; its workspaces move as
// on an unplug.
func RemoveOutput(name string) error {
	return call(request{Request: "removeoutput", Name: name}, nil)
}

// OffersVirtualOutputs reports whether the running lys-wm makes virtual
// outputs: it asks for one without a mode, which a lys-wm that knows the
// request refuses for the mode and an older one refuses as unknown.
func OffersVirtualOutputs() (bool, error) {
	err := call(request{Request: createOutput}, nil)
	var rejected *RejectedError
	switch {
	case err == nil:
		return false, fmt.Errorf("lys-wm: %s without a mode succeeded", createOutput)
	case !errors.As(err, &rejected):
		return false, err
	}
	return !strings.Contains(rejected.Message, "unknown request"), nil
}
