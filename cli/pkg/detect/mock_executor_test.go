package detect

import (
	"fmt"
	"strings"
)

// MockExecutor provides canned responses for unit testing without a live cluster.
type MockExecutor struct {
	Responses map[string]string
	Errors    map[string]error
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
	if err, ok := m.Errors[k]; ok {
		return "", err
	}
	if resp, ok := m.Responses[k]; ok {
		return resp, nil
	}
	// Try prefix matching for flexibility
	for pattern, resp := range m.Responses {
		if strings.HasPrefix(k, pattern) {
			return resp, nil
		}
	}
	return "", fmt.Errorf("mock: no response for %q", k)
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
