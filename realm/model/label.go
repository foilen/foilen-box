package model

import "strings"

func ShortID(id string) string {
	if len(id) <= 6 {
		return "[" + id + "]"
	}
	return "[" + id[len(id)-6:] + "]"
}

func (p PeerInfo) Label() string {
	var parts []string
	if p.Hostname != "" {
		parts = append(parts, p.Hostname)
	}
	if p.Description != "" {
		parts = append(parts, "("+p.Description+")")
	}
	parts = append(parts, ShortID(p.ID))
	return strings.Join(parts, " ")
}

func (g Group) Label() string {
	return g.Name + " " + ShortID(g.KeyPair.ID)
}

func (i Identity) Label() string {
	return i.Name + " " + ShortID(i.KeyPair.ID)
}

func GroupLabel(groups []Group, id string) string {
	if g, ok := FindGroupByID(groups, id); ok {
		return g.Label()
	}
	return ShortID(id)
}
