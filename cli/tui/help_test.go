package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// bindingsOf is every key.Binding field of a keyMap struct.
func bindingsOf(keyMap any) map[string]key.Binding {
	out := map[string]key.Binding{}
	v := reflect.ValueOf(keyMap)
	for i := range v.NumField() {
		if b, ok := v.Field(i).Interface().(key.Binding); ok {
			out[v.Type().Field(i).Name] = b
		}
	}
	return out
}

// helpScreens renders the help of every screen a model has.
func helpScreens(t *testing.T) map[string][]string {
	t.Helper()
	dash := loadedDashboard(t)
	var tabs []string
	addTabs := func(m Model) { tabs = append(tabs, plain(renderHelp(m.helpBindings(), m.width))) }
	for _, k := range []string{"1", "2", "3", "4"} {
		dash = update(t, dash, runes(k))
		addTabs(dash)
	}
	dash.produce.view = produceInput
	addTabs(dash)
	dash.produce.view = produceTopicSelect
	dash = update(t, dash, runes("2"))
	dash = update(t, dash, runes("/"))
	addTabs(dash)

	render := func(b []key.Binding) string { return plain(renderHelp(b, 200)) }
	var topics, groups, produce []string
	for _, v := range []topicsView{topicsList, topicsDetail, topicsConsume} {
		topics = append(topics, render(topicsModel{view: v}.helpBindings()))
	}
	for _, v := range []groupsView{groupsList, groupsDetail} {
		groups = append(groups, render(groupsModel{view: v}.helpBindings()))
	}
	for _, v := range []produceView{produceTopicSelect, produceInput} {
		produce = append(produce, render(produceModel{view: v}.helpBindings()))
	}

	var lab []string
	m := NewLab(nil, "")
	m.iterations = []labIteration{{Number: 1}, {Number: 2}}
	m.lastError = errors.New("boom")
	m.view = labHistory
	lab = append(lab, HelpLines(m.helpBindings(), 80))
	m.view = labPinSelect
	lab = append(lab, HelpLines(m.helpBindings(), 80))
	m.running = true
	lab = append(lab, HelpLines(m.helpBindings(), 80))
	for i := range lab {
		lab[i] = plain(lab[i])
	}

	return map[string][]string{
		"tabs":    tabs,
		"topics":  topics,
		"groups":  groups,
		"brokers": {render(brokersModel{}.helpBindings())},
		"produce": produce,
		"lab":     lab,
	}
}

// Every binding of every keyMap shows in the help of some screen of its
// model: the help is generated from the bindings Update matches.
func TestEveryBindingAppearsInTheHelp(t *testing.T) {
	screens := helpScreens(t)
	for name, keyMap := range map[string]any{
		"tabs":    tabsKeys(),
		"topics":  topicsKeys(),
		"groups":  groupsKeys(),
		"brokers": brokersKeys(),
		"produce": produceKeys(),
		"lab":     labKeys(),
	} {
		all := strings.Join(screens[name], "\n")
		// The help wraps; an entry may break across a line only at its
		// separator, so compare with the lines joined.
		flat := strings.Join(strings.Fields(all), " ")
		for field, b := range bindingsOf(keyMap) {
			h := b.Help()
			entry := strings.Join(strings.Fields(h.Key+" "+h.Desc), " ")
			if h.Key == "" || !strings.Contains(flat, entry) {
				t.Errorf("%s.%s (%q) is in no help screen:\n%s", name, field, entry, all)
			}
		}
	}
}

// The tab help used to have no r, and offered Enter on the brokers tab,
// which has no detail.
func TestTabHelpMatchesTheKeys(t *testing.T) {
	screens := helpScreens(t)["tabs"]
	brokers, topics, groups, produce := screens[0], screens[1], screens[2], screens[3]
	for i, s := range []string{brokers, topics, groups, produce} {
		mustContain(t, s, "r refresh", "q quit", "1-4 go to tab")
		if i != 1 && strings.Contains(s, "/ filter") {
			t.Errorf("screen %d offers the topic filter:\n%s", i, s)
		}
	}
	if strings.Contains(brokers, "enter") {
		t.Errorf("the brokers tab has no enter, but its help offers one:\n%s", brokers)
	}
	mustContain(t, topics, "/ filter", "enter topic detail")
}

// Esc acts differently per view; each view's help says what it does there.
func TestEachViewSaysWhatEscDoes(t *testing.T) {
	render := func(b []key.Binding) string { return plain(renderHelp(b, 300)) }
	for name, want := range map[string]string{
		render(topicsModel{view: topicsDetail}.helpBindings()):  "esc/backspace back to the list",
		render(topicsModel{view: topicsConsume}.helpBindings()): "esc/backspace stop tailing",
		render(groupsModel{view: groupsDetail}.helpBindings()):  "esc/backspace back to the list",
		render(produceModel{view: produceInput}.helpBindings()): "esc back to the topic list",
		render(Model{filtering: true}.helpBindings()):           "esc clear filter",
	} {
		if !strings.Contains(name, want) {
			t.Errorf("want %q in %q", want, name)
		}
	}
	lab := NewLab(nil, "")
	lab.view = labHistory
	mustContain(t, plain(HelpLines(lab.helpBindings(), 300)), "esc back to the results")
}

// w/W and l/L are different keys; the help names the case.
func TestLabHelpNamesTheCaseOfCaseSensitivePairs(t *testing.T) {
	m := NewLab(nil, "")
	help := plain(HelpLines(m.helpBindings(), 400))
	mustContain(t, help, "w (lower-case) save session", "W (shift+w) warmup", "L (shift+l) load session", "/l (lower-case) next value")

	for k, wantField := range map[string]string{"w": "Save", "W": "Warmup", "l": "Right", "L": "Load"} {
		var matched []string
		for field, b := range bindingsOf(labKeys()) {
			if key.Matches(runes(k), b) {
				matched = append(matched, field)
			}
		}
		if len(matched) != 1 || matched[0] != wantField {
			t.Errorf("%q matches %v, want only %s", k, matched, wantField)
		}
	}
}

func TestLabHelpFollowsTheState(t *testing.T) {
	m := NewLab(nil, "")
	help := plain(HelpLines(m.helpBindings(), 400))
	for _, absent := range []string{"c compare", "r retry", "esc", "x cancel"} {
		if strings.Contains(help, absent) {
			t.Errorf("a fresh lab offers %q:\n%s", absent, help)
		}
	}
	m.running = true
	help = plain(HelpLines(m.helpBindings(), 400))
	mustContain(t, help, "x cancel the run", "ctrl+c quit (cancels the run)")
	if strings.Contains(help, "enter") {
		t.Errorf("a running lab offers enter:\n%s", help)
	}
}

func TestWrapHelp(t *testing.T) {
	entries := []string{"aaaa", "bbbb", "cccc", "dd"}
	for _, tc := range []struct {
		width int
		want  []string
	}{
		{0, []string{"aaaa · bbbb · cccc · dd"}},
		{100, []string{"aaaa · bbbb · cccc · dd"}},
		{11, []string{"aaaa · bbbb", "cccc · dd"}},
		{10, []string{"aaaa", "bbbb", "cccc · dd"}},
		{2, []string{"aaaa", "bbbb", "cccc", "dd"}},
	} {
		got := wrapHelp(entries, " · ", tc.width)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("width %d: got %q, want %q", tc.width, got, tc.want)
		}
		for _, l := range got {
			if tc.width > 0 && lipgloss.Width(l) > max(tc.width, 4) {
				t.Errorf("width %d: line %q is wider", tc.width, l)
			}
		}
	}
	if wrapHelp(nil, " · ", 10) != nil {
		t.Error("no entries gave lines")
	}
}

// The dashboard's help wraps in a narrow terminal and the view keeps to the
// window's height.
func TestTabHelpWrapsInsideTheWindow(t *testing.T) {
	m := loadedDashboard(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Errorf("the view is %d lines in a 24-line window", lines)
	}
	for _, l := range strings.Split(view, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("a line is %d cells wide in an 80-cell window: %q", w, plain(l))
		}
	}
	mustContain(t, plain(view), "q quit")
}

func TestHint(t *testing.T) {
	k := topicsKeys()
	if got := hint(k.Consume, k.Back); got != "c consume  ·  esc/backspace back to the list" {
		t.Errorf("hint = %q", got)
	}
	if got := helpEntries([]key.Binding{key.NewBinding(key.WithKeys("z"))}); len(got) != 0 {
		t.Errorf("a binding without help gave %q", got)
	}
}
