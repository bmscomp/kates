package proc

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// Fake is a scripted Runner for tests. It answers each call with the first
// script whose prefix matches the call, records every call it receives, and
// is safe for concurrent use, since commands such as `deploy status` run
// their checks in parallel.
//
// A prefix is a command line's leading words: "kubectl get pods" matches
// `kubectl get pods -n kafka -o json` and not `kubectl get podsecuritypolicy`.
// A call no script matches gets empty output and no error, unless Unmatched
// is set, which it then returns wrapped in an *Error.
type Fake struct {
	// Unmatched, when set, is the error an unscripted call fails with.
	Unmatched error

	mu      sync.Mutex
	scripts []script
	calls   []Call
}

// Call is one command a Fake was asked to run.
type Call struct {
	Name  string
	Args  []string
	Stdin string
	// Ctx is the context the call was made with, so a test can check the
	// deadline a command gave it.
	Ctx context.Context
}

// String is the call as a shell line.
func (c Call) String() string { return String(c.Name, c.Args) }

type script struct {
	prefix []string
	reply  func(Call) ([]byte, error)
}

// On answers calls matching prefix with stdout and no error. Scripts are
// tried in the order they were added, so add the specific before the general.
func (f *Fake) On(prefix, stdout string) *Fake {
	return f.Handle(prefix, func(Call) ([]byte, error) { return []byte(stdout), nil })
}

// Fail answers calls matching prefix with exit status 1 and stderr.
func (f *Fake) Fail(prefix, stderr string) *Fake {
	return f.Handle(prefix, func(c Call) ([]byte, error) {
		return nil, &Error{Name: c.Name, Args: c.Args, Stderr: stderr, Err: errExit1, code: 1}
	})
}

// Handle answers calls matching prefix with whatever reply returns. A reply
// error that is not already an *Error is wrapped in one, so callers see the
// same type the OS runner gives them.
func (f *Fake) Handle(prefix string, reply func(Call) ([]byte, error)) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, script{prefix: strings.Fields(prefix), reply: reply})
	return f
}

// Calls is every call so far, in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Lines is every call so far as shell lines, for a reflect.DeepEqual against
// what a test expects.
func (f *Fake) Lines() []string {
	calls := f.Calls()
	lines := make([]string, len(calls))
	for i, c := range calls {
		lines[i] = c.String()
	}
	return lines
}

// Reset forgets the recorded calls, keeping the scripts.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// errExit1 stands in for the *exec.ExitError a real command would produce.
var errExit1 = errors.New("exit status 1")

func (f *Fake) answer(c Call) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	var reply func(Call) ([]byte, error)
	for _, s := range f.scripts {
		if matches(s.prefix, c) {
			reply = s.reply
			break
		}
	}
	unmatched := f.Unmatched
	f.mu.Unlock()

	if reply == nil {
		if unmatched != nil {
			return nil, &Error{Name: c.Name, Args: c.Args, Err: unmatched}
		}
		return nil, nil
	}
	out, err := reply(c)
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			err = &Error{Name: c.Name, Args: c.Args, Err: err}
		}
	}
	return out, err
}

func matches(prefix []string, c Call) bool {
	words := append([]string{c.Name}, c.Args...)
	if len(prefix) > len(words) {
		return false
	}
	for i, p := range prefix {
		if words[i] != p {
			return false
		}
	}
	return true
}

// Run implements Runner.
func (f *Fake) Run(ctx context.Context, name string, args ...string) error {
	_, err := f.answer(Call{Name: name, Args: args, Ctx: ctx})
	return err
}

// Output implements Runner.
func (f *Fake) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f.answer(Call{Name: name, Args: args, Ctx: ctx})
}

// Combined implements Runner.
func (f *Fake) Combined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f.answer(Call{Name: name, Args: args, Ctx: ctx})
}

// OutputStdin implements Runner.
func (f *Fake) OutputStdin(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	return f.answer(Call{Name: name, Args: args, Stdin: stdin, Ctx: ctx})
}
