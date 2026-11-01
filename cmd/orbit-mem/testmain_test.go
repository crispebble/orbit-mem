package main

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// defaultSchedule records the OS schedule commands (launchctl, schtasks) a test ran without
// its own fake (fakeSchedule): no test reaches the real launchd or Task Scheduler.
var defaultSchedule struct {
	sync.Mutex
	calls []string
}

func TestMain(m *testing.M) {
	scheduleRun = func(name string, args ...string) (string, error) {
		defaultSchedule.Lock()
		defer defaultSchedule.Unlock()
		defaultSchedule.calls = append(defaultSchedule.calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	// 훅이 뒤에서 띄우는 사본 프로세스(backup auto)도 기본은 가짜다: 시험 바이너리를 다시 띄우지 않는다.
	startBackground = func(args ...string) error { return nil }
	os.Exit(m.Run())
}

func TestMain_예약_명령은_기본으로_가짜다(t *testing.T) {
	defaultSchedule.Lock()
	n := len(defaultSchedule.calls)
	defaultSchedule.Unlock()
	if _, err := scheduleRun("launchctl", "print", "gui/0/x"); err != nil {
		t.Fatal(err)
	}
	defaultSchedule.Lock()
	defer defaultSchedule.Unlock()
	if len(defaultSchedule.calls) != n+1 || defaultSchedule.calls[n] != "launchctl print gui/0/x" {
		t.Fatalf("calls = %v", defaultSchedule.calls)
	}
}
