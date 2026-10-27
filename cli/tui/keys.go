package tui

import "github.com/charmbracelet/bubbles/key"

// One keyMap per model. Update matches keys against these bindings and the
// help line is rendered from the same bindings, so a key cannot be handled
// without being documented, or documented without being handled. The maps
// are built per call, not once: the arrow glyphs in their help follow
// --plain, which is applied after package init.

func bind(helpKey, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, desc))
}

// relabel is b with other help: the same keys, described for another view.
func relabel(b key.Binding, helpKey, desc string) key.Binding {
	b.SetHelp(helpKey, desc)
	return b
}

// tabsKeyMap is the dashboard's own keys: quit, tab switching and the topic
// filter's editing keys.
type tabsKeyMap struct {
	Quit    key.Binding
	NextTab key.Binding
	PrevTab key.Binding
	JumpTab key.Binding

	Look   key.Binding
	Colors key.Binding

	FilterApply key.Binding
	FilterClear key.Binding
	FilterErase key.Binding
	FilterType  key.Binding // help only: typed runes are the filter text
}

func tabsKeys() tabsKeyMap {
	return tabsKeyMap{
		Quit:    bind("q", "quit", "q", "ctrl+c"),
		NextTab: bind("tab", "next tab", "tab"),
		PrevTab: bind("shift+tab", "previous tab", "shift+tab"),
		JumpTab: bind("1-4", "go to tab", "1", "2", "3", "4"),
		Look:    lookKey(),
		Colors:  colorsKey(),

		FilterApply: bind("enter", "apply filter", "enter"),
		FilterClear: bind("esc", "clear filter", "esc"),
		FilterErase: bind("backspace", "delete a character", "backspace"),
		FilterType:  key.NewBinding(key.WithHelp("type", "to filter")),
	}
}

// lookKey switches to the next look on the spot.
func lookKey() key.Binding { return bind("T (shift+t)", "next look", "T") }

// colorsKey switches to the next color theme on the spot.
func colorsKey() key.Binding { return bind("C (shift+c)", "next colors", "C") }

// navKeys are the vertical cursor bindings every list shares.
func navKeys() (up, down key.Binding) {
	g := glyphs()
	return bind(g.KeyUp+"/k", "up", "up", "k"), bind(g.KeyDown+"/j", "down", "down", "j")
}

type topicsKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Detail  key.Binding
	Refresh key.Binding
	Filter  key.Binding // handled by the tab model, which owns the filter
	Back    key.Binding
	Consume key.Binding
	Stop    key.Binding
}

func topicsKeys() topicsKeyMap {
	up, down := navKeys()
	return topicsKeyMap{
		Up:      up,
		Down:    down,
		Detail:  bind("enter", "topic detail", "enter"),
		Refresh: bind("r", "refresh", "r"),
		Filter:  bind("/", "filter", "/"),
		Back:    bind("esc/backspace", "back to the list", "esc", "backspace"),
		Consume: bind("c", "consume", "c"),
		Stop:    bind("esc/backspace", "stop tailing", "esc", "backspace"),
	}
}

type groupsKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Detail  key.Binding
	Refresh key.Binding
	Back    key.Binding
}

func groupsKeys() groupsKeyMap {
	up, down := navKeys()
	return groupsKeyMap{
		Up:      up,
		Down:    down,
		Detail:  bind("enter", "offsets & lag", "enter"),
		Refresh: bind("r", "refresh", "r"),
		Back:    bind("esc/backspace", "back to the list", "esc", "backspace"),
	}
}

type brokersKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Refresh key.Binding
}

func brokersKeys() brokersKeyMap {
	up, down := navKeys()
	return brokersKeyMap{Up: up, Down: down, Refresh: bind("r", "refresh", "r")}
}

type produceKeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Select    key.Binding
	Refresh   key.Binding
	NextField key.Binding
	Send      key.Binding
	Back      key.Binding
	Quit      key.Binding // ctrl+c only: q is text in the input
}

func produceKeys() produceKeyMap {
	up, down := navKeys()
	return produceKeyMap{
		Up:        up,
		Down:      down,
		Select:    bind("enter", "produce to topic", "enter"),
		Refresh:   bind("r", "refresh", "r"),
		NextField: bind("tab", "switch field", "tab"),
		Send:      bind("enter", "send", "enter"),
		Back:      bind("esc", "back to the topic list", "esc"),
		Quit:      bind("ctrl+c", "quit", "ctrl+c"),
	}
}

// labKeyMap holds the lab's keys. Two pairs differ only by case — w saves
// and W sets the warmup, l moves right and L loads — so their help names
// the case.
type labKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Left    key.Binding
	Right   key.Binding
	Run     key.Binding
	Preset  key.Binding
	Diff    key.Binding
	Compare key.Binding
	Sweep   key.Binding
	Median  key.Binding
	Warmup  key.Binding
	Export  key.Binding
	Save    key.Binding
	Load    key.Binding
	Retry   key.Binding
	Back    key.Binding
	Look    key.Binding
	Colors  key.Binding
	Quit    key.Binding

	// While a run is in flight only these act.
	Cancel    key.Binding
	ForceQuit key.Binding
}

func labKeys() labKeyMap {
	g := glyphs()
	return labKeyMap{
		Up:      bind(g.KeyUp+"/k", "previous parameter", "up", "k"),
		Down:    bind(g.KeyDown+"/j", "next parameter", "down", "j"),
		Left:    bind(g.KeyLeft+"/h", "previous value", "left", "h"),
		Right:   bind(g.KeyRight+"/l (lower-case)", "next value", "right", "l"),
		Run:     bind("enter", "run", "enter"),
		Preset:  bind("p", "preset", "p"),
		Diff:    bind("d", "diff", "d"),
		Compare: bind("c", "compare", "c"),
		Sweep:   bind("s", "sweep", "s"),
		Median:  bind("m", "median of 3", "m"),
		Warmup:  bind("W (shift+w)", "warmup", "W"),
		Export:  bind("e", "export CSV", "e"),
		Save:    bind("w (lower-case)", "save session", "w"),
		Load:    bind("L (shift+l)", "load session", "L"),
		Retry:   bind("r", "retry", "r"),
		Back:    bind("esc", "back to the results", "esc", "escape"),
		Look:    lookKey(),
		Colors:  colorsKey(),
		Quit:    bind("q", "quit", "q", "ctrl+c"),

		Cancel:    bind("x", "cancel the run", "x"),
		ForceQuit: bind("ctrl+c", "quit (cancels the run)", "ctrl+c"),
	}
}
