package hook

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/crispebble/orbit-mem/internal/host"
)

// Runner runs hooks on one Service.
type Runner struct {
	Service *host.Service
	Options host.Options // 활동 시각 등 계약에 없는 선택지
	// Note may change a hook's context before it is printed (the lines the assembly point adds).
	// It is called only for a context the last operation gave. nil leaves the context as it is.
	Note func(ev Event, plan Plan, text string) string
	// Failed is told of each operation that ended in an error and of a panic (op ""). nil: nothing.
	Failed func(op string, err *host.Error)
}

// Run runs one hook and prints its output to w. The operations run in the plan's order, each
// within its own time limit (hostTimeouts); one that fails does not stop the next. Only the last
// one's context is printed, at once, and then its records are waited for until its limit. Any
// error, timeout or panic prints nothing. Run never panics.
func (r Runner) Run(a Adapter, ev Event, input []byte, w io.Writer) {
	defer func() {
		if p := recover(); p != nil {
			r.failed("", &host.Error{Code: host.CodeInternal, Message: fmt.Sprint(p)})
		}
	}()
	if r.Service == nil {
		return
	}
	plan := a.Plan(ev, input)
	for i, st := range plan.Steps {
		last := i == len(plan.Steps)-1
		r.step(st, func(out any) {
			if !last {
				return
			}
			res, ok := out.(host.Recall)
			if !ok || res.Skipped != "" { // 읽지 못하는 DB(없음·새 판·옛 판)나 사람 요청이 아님: 빈 문맥
				return
			}
			text := res.Text
			if r.Note != nil {
				text = r.Note(ev, plan, text)
			}
			if s := a.Output(ev, text); s != "" {
				fmt.Fprintln(w, s)
			}
		})
	}
}

// step runs one operation: use gets its result as soon as it is computed; then the records are
// waited for until the operation's limit, a panic in use included.
func (r Runner) step(st Step, use func(out any)) {
	run := r.Service.Start(st.Op, st.Request, r.Options)
	defer run.Finish()
	out, err := run.Result()
	if err != nil {
		r.failed(st.Op, err)
		return
	}
	use(out)
}

func (r Runner) failed(op string, err *host.Error) {
	if r.Failed == nil {
		return
	}
	defer func() { _ = recover() }()
	r.Failed(op, err)
}

// ErrInputWait is the input of a hook that did not end in time (the host never closed it).
var ErrInputWait = errors.New("hook input did not end in time")

// ReadInput reads a hook's input up to limit bytes. A reader that has not ended within wait gives
// ErrInputWait, and one that panics an error: the hook then gives up without output. A read error
// keeps what was read (the hooks have always gone on with it).
func ReadInput(rd io.Reader, limit int64, wait time.Duration) ([]byte, error) {
	type read struct {
		b   []byte
		err error
	}
	got := make(chan read, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				got <- read{err: fmt.Errorf("hook input: %v", p)}
			}
		}()
		b, _ := io.ReadAll(io.LimitReader(rd, limit))
		got <- read{b: b}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-got:
		return r.b, r.err
	case <-timer.C:
		return nil, ErrInputWait
	}
}
