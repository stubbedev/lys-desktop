package swaytest

import (
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

func TestAMsgToAStoppedSwayReportsItsStateAndCPU(t *testing.T) {
	sway := Start(t)
	Msg(t, "-t", "get_version")
	if err := sway.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	_, err := msg(os.Getenv("XDG_RUNTIME_DIR"), "-t", "get_version")
	if err == nil {
		t.Fatal("a stopped sway answered swaymsg")
	}
	report := err.Error()
	t.Log(report)
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^  State: T \(stopped\)$`),
		regexp.MustCompile(`(?m)^  wchan: \S+$`),
		regexp.MustCompile(`(?m)^  CPU \(user\+system\): (\S+) when swaymsg started, (\S+) at failure, 0s in the \S+ between$`),
		regexp.MustCompile(`(?m)^  threads \([1-9]\d*\):$`),
		regexp.MustCompile(`(?m)^  load average: [\d.]+ [\d.]+ [\d.]+ \d+/\d+ \d+$`),
		regexp.MustCompile(`(?m)^  online CPUs: [1-9]\d* \(\S+\), [1-9]\d* usable by this process$`),
		regexp.MustCompile(`(?m)^backtrace`),
		regexp.MustCompile(`(?m)^sway's log `),
	} {
		if !want.MatchString(report) {
			t.Errorf("the report lacks %s", want)
		}
	}
	threads := regexp.MustCompile(`(?m)^    \d+ .*: state (\S+), wchan \S+, CPU \S+$`).FindAllStringSubmatch(report, -1)
	if len(threads) == 0 {
		t.Error("the report lists no thread")
	}
	for _, thread := range threads {
		if thread[1] != "T" {
			t.Errorf("a thread of the stopped sway is not stopped: %s", strings.TrimSpace(thread[0]))
		}
	}
}
