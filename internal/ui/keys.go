package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Quit, Help, Refresh                   key.Binding
	Containers, Images, Volumes, Networks key.Binding
	Up, Down, Top, Bottom, PageUp, PageDn key.Binding
	Filter, Clear, CycleFilter            key.Binding
	NextTab, PrevTab, Details             key.Binding
	ScrollUp, ScrollDown                  key.Binding

	Start, Stop, Restart, Pause, Remove key.Binding
	Logs, Exec, Stats, Inspect          key.Binding

	Confirm, Cancel key.Binding
}

func kb(keys []string, help, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

func defaultKeys() keyMap {
	return keyMap{
		Quit:    kb([]string{"q", "ctrl+c"}, "q", "quit"),
		Help:    kb([]string{"?"}, "?", "help"),
		Refresh: kb([]string{"ctrl+r"}, "ctrl+r", "refresh now"),

		Containers: kb([]string{"1"}, "1", "containers"),
		Images:     kb([]string{"2"}, "2", "images"),
		Volumes:    kb([]string{"3"}, "3", "volumes"),
		Networks:   kb([]string{"4"}, "4", "networks"),

		Up:     kb([]string{"up", "k"}, "↑ k", "move up"),
		Down:   kb([]string{"down", "j"}, "↓ j", "move down"),
		Top:    kb([]string{"home", "g"}, "g", "first"),
		Bottom: kb([]string{"end", "G"}, "G", "last"),
		PageUp: kb([]string{"pgup", "ctrl+u"}, "pgup", "page up"),
		PageDn: kb([]string{"pgdown", "ctrl+d"}, "pgdn", "page down"),

		Filter:      kb([]string{"/"}, "/", "filter list"),
		Clear:       kb([]string{"esc"}, "Esc", "clear filter / close"),
		CycleFilter: kb([]string{"f"}, "f", "all / running / stopped"),

		NextTab:    kb([]string{"tab"}, "Tab", "next detail tab"),
		PrevTab:    kb([]string{"shift+tab"}, "S-Tab", "previous tab"),
		Details:    kb([]string{"enter"}, "Enter", "toggle details"),
		ScrollUp:   kb([]string{"K", "shift+up"}, "K", "scroll details up"),
		ScrollDown: kb([]string{"J", "shift+down"}, "J", "scroll details down"),

		Start:   kb([]string{"s"}, "s", "start"),
		Stop:    kb([]string{"x"}, "x", "stop"),
		Restart: kb([]string{"r"}, "r", "restart"),
		Pause:   kb([]string{"p"}, "p", "pause / unpause"),
		Remove:  kb([]string{"d", "delete"}, "d", "remove (asks first)"),
		Logs:    kb([]string{"l"}, "l", "logs"),
		Exec:    kb([]string{"e"}, "e", "exec shell"),
		Stats:   kb([]string{"t"}, "t", "live stats"),
		Inspect: kb([]string{"i"}, "i", "inspect JSON"),

		Confirm: kb([]string{"y", "enter"}, "y", "confirm"),
		Cancel:  kb([]string{"n", "esc", "q"}, "Esc", "cancel"),
	}
}

// footerHint is one chip in the key-hint bar.
type footerHint struct{ key, label string }

func (m Model) footerHints() []footerHint {
	if m.view == viewContainers {
		return []footerHint{
			{"1-4", "views"}, {"↑↓", "move"}, {"s", "start"}, {"x", "stop"}, {"r", "restart"},
			{"p", "pause"}, {"d", "remove"}, {"l", "logs"}, {"e", "exec"}, {"t", "stats"},
			{"i", "inspect"}, {"/", "filter"}, {"?", "help"}, {"q", "quit"},
		}
	}
	return []footerHint{{"1-4", "views"}, {"↑↓", "move"}, {"d", "remove"}, {"/", "filter"}, {"?", "help"}, {"q", "quit"}}
}

// helpRows is the content of the ? overlay, in two columns.
func (m Model) helpRows() [][2]string {
	k := m.keys
	rows := [][2]string{{"1-4", "switch view"}}
	for _, b := range []key.Binding{
		k.Up, k.Down, k.Top, k.Bottom, k.Filter, k.Clear, k.CycleFilter, k.NextTab, k.Details, k.ScrollDown,
		k.Start, k.Stop, k.Restart, k.Pause, k.Remove, k.Logs, k.Exec, k.Stats, k.Inspect, k.Refresh, k.Quit,
	} {
		h := b.Help()
		rows = append(rows, [2]string{h.Key, h.Desc})
	}
	return rows
}
