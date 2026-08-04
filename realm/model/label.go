package model

import "strings"

// ShortID mirrors util.js's shortId: the last 6 characters of id, bracketed,
// so a resource reads identically in logs and in the webui.
func ShortID(id string) string {
	if len(id) <= 6 {
		return "[" + id + "]"
	}
	return "[" + id[len(id)-6:] + "]"
}

// Label renders "hostname (description) [shortid]", omitting hostname/description if unset.
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

// GroupLabel looks up id in groups by KeyPair.ID and returns its Label, or
// just ShortID(id) if id isn't among groups.
func GroupLabel(groups []Group, id string) string {
	for _, g := range groups {
		if g.KeyPair.ID == id {
			return g.Label()
		}
	}
	return ShortID(id)
}

// IdentityLabel looks up id in identities by KeyPair.ID and returns its
// Label, or just ShortID(id) if id isn't among identities.
func IdentityLabel(identities []Identity, id string) string {
	for _, i := range identities {
		if i.KeyPair.ID == id {
			return i.Label()
		}
	}
	return ShortID(id)
}
