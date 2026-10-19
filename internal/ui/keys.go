package ui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
)

// keyMap holds the app-level bindings. Movement inside the list is handled by
// the table's own key map (see tableKeyMap).
type keyMap struct {
	Details key.Binding
	Refresh key.Binding
	Quit    key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Details: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Refresh: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// shortHelp is what the footer shows, in order.
func (k keyMap) shortHelp() []key.Binding {
	move := key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move"))
	return []key.Binding{move, k.Details, k.Refresh, k.Quit}
}

// tableKeyMap is the table's default map without the single-letter bindings
// (d, u, f, b, space) so those letters stay free for container actions.
func tableKeyMap() table.KeyMap {
	return table.KeyMap{
		LineUp:       key.NewBinding(key.WithKeys("up", "k")),
		LineDown:     key.NewBinding(key.WithKeys("down", "j")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		GotoTop:      key.NewBinding(key.WithKeys("home", "g")),
		GotoBottom:   key.NewBinding(key.WithKeys("end", "G")),
	}
}
