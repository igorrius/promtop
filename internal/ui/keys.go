package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, Left, Right  key.Binding
	PgUp, PgDown           key.Binding
	Home, End              key.Binding
	PrevSec, NextSec       key.Binding
	Open, Back             key.Binding
	Filter, Clear, All     key.Binding
	Pause, Longer, Shorter key.Binding
	Help, Quit             key.Binding

	// Detail view
	Series, Line key.Binding
}

func newKeyMap() keyMap {
	b := func(keys []string, k, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(k, desc))
	}
	return keyMap{
		Up:      b([]string{"up", "k"}, "↑/k", "up"),
		Down:    b([]string{"down", "j"}, "↓/j", "down"),
		Left:    b([]string{"left", "h", "shift+tab"}, "←/h", "left"),
		Right:   b([]string{"right", "l", "tab"}, "→/l", "right"),
		PgUp:    b([]string{"pgup"}, "pgup", "page up"),
		PgDown:  b([]string{"pgdown"}, "pgdn", "page down"),
		Home:    b([]string{"home", "g"}, "g", "first"),
		End:     b([]string{"end", "G"}, "G", "last"),
		PrevSec: b([]string{"["}, "[", "prev section"),
		NextSec: b([]string{"]"}, "]", "next section"),
		Open:    b([]string{"enter"}, "enter", "details"),
		Back:    b([]string{"esc", "backspace"}, "esc", "back"),
		Filter:  b([]string{"/"}, "/", "filter"),
		Clear:   b([]string{"c"}, "c", "clear filter"),
		All:     b([]string{"a"}, "a", "all/curated"),
		Pause:   b([]string{"p", "space"}, "p", "pause"),
		Longer:  b([]string{"+", "="}, "+", "longer interval"),
		Shorter: b([]string{"-", "_"}, "-", "shorter interval"),
		Help:    b([]string{"?"}, "?", "help"),
		Quit:    b([]string{"q", "ctrl+c"}, "q", "quit"),
		Series:  b([]string{"up", "down"}, "↑↓", "series"),
		Line:    b([]string{"left", "right", "tab"}, "←→", "line"),
	}
}

// Combined entries for the short help. They need keys: help skips bindings
// without any as disabled.
var (
	move     = key.NewBinding(key.WithKeys("up", "down", "left", "right"), key.WithHelp("←↑↓→", "move"))
	sections = key.NewBinding(key.WithKeys("[", "]"), key.WithHelp("[ ]", "section"))
	interval = key.NewBinding(key.WithKeys("+", "-"), key.WithHelp("+/-", "interval"))
)

func (k keyMap) gridHelp() []key.Binding {
	return []key.Binding{
		move, k.Open, k.Filter, k.All, sections, k.Pause, interval, k.Help, k.Quit,
	}
}

func (k keyMap) detailHelp() []key.Binding {
	return []key.Binding{k.Series, k.Line, k.Back, k.Pause, interval, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right, k.PgUp, k.PgDown},
		{k.Home, k.End, k.PrevSec, k.NextSec, k.Open, k.Back},
		{k.Filter, k.Clear, k.All, k.Pause, k.Longer, k.Shorter},
		{k.Help, k.Quit},
	}
}
