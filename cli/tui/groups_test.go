package tui

import (
	"fmt"
	"strings"
	"testing"
)

// The list drew every group, so with more groups than rows the cursor
// scrolled off the screen; the view keeps a window around it, as topics do.
func TestGroupsListKeepsTheCursorVisible(t *testing.T) {
	m := groupsModel{width: 120, height: 10}
	for i := 0; i < 50; i++ {
		m.groups = append(m.groups, groupEntry{GroupID: fmt.Sprintf("group-%02d", i), State: "Stable", Members: "1"})
	}

	m.cursor = 45
	view := m.viewList()
	if !strings.Contains(view, "group-45") {
		t.Errorf("cursor on group-45, but the view does not show it:\n%s", view)
	}
	if strings.Contains(view, "group-00") {
		t.Errorf("the view shows group-00 as well as group-45; the window is the whole list:\n%s", view)
	}
	if !strings.Contains(view, "▸") {
		t.Errorf("the view has no cursor marker:\n%s", view)
	}

	m.cursor = 0
	view = m.viewList()
	if !strings.Contains(view, "group-00") || strings.Contains(view, "group-45") {
		t.Errorf("cursor on group-00: the view should start at the top:\n%s", view)
	}
}
