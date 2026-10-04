package detect

import (
	"fmt"
	"strings"
	"sync"
)

// MockExecutor provides canned responses for unit testing without a live cluster.
type MockExecutor struct {
	Responses map[string]string
	Errors    map[string]error

	mu    sync.Mutex
	calls []string
}

func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		Responses: make(map[string]string),
		Errors:    make(map[string]error),
	}
}

func (m *MockExecutor) key(name string, args ...string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}

func (m *MockExecutor) Exec(name string, args ...string) (string, error) {
	k := m.key(name, args...)
	m.mu.Lock()
	m.calls = append(m.calls, k)
	m.mu.Unlock()

	var matched bool
	var resErr error
	if err, ok := m.Errors[k]; ok {
		resErr = err
		matched = true
	} else {
		for pattern, err := range m.Errors {
			if strings.HasPrefix(k, pattern) || strings.Contains(k, pattern) {
				resErr = err
				matched = true
				break
			}
		}
	}

	var resResp string
	if resp, ok := m.Responses[k]; ok {
		resResp = resp
		matched = true
	} else {
		for pattern, resp := range m.Responses {
			if strings.HasPrefix(k, pattern) || strings.Contains(k, pattern) {
				resResp = resp
				matched = true
				break
			}
		}
	}

	if matched {
		return resResp, resErr
	}
	return "", fmt.Errorf("mock: no response for %q", k)
}

// Calls returns every command Exec was given, in the order the calls arrived,
// each joined with spaces as the response keys are.
func (m *MockExecutor) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

func (m *MockExecutor) LookPath(file string) (string, error) {
	return "/usr/local/bin/" + file, nil
}

func (m *MockExecutor) Set(stdout string, name string, args ...string) {
	k := m.key(name, args...)
	m.Responses[k] = stdout
}

func (m *MockExecutor) SetError(err error, name string, args ...string) {
	k := m.key(name, args...)
	m.Errors[k] = err
}
