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
	
	var matched bool
	var resErr error
	if err, ok := m.Errors[k]; ok {
		resErr = err
		matched = true
	} else {
		for pattern, err := range m.Errors {
			if strings.HasPrefix(k, pattern) {
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
			if strings.HasPrefix(k, pattern) {
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
