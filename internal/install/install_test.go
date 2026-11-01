package install

import (
	"strings"
)

type fakeRun struct{ calls []string }

func (f *fakeRun) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

