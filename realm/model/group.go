package model

type Group struct {
	Name    string  `json:"name"`
	KeyPair KeyPair `json:"keyPair"`
}

func FindGroupByID(groups []Group, id string) (Group, bool) {
	for _, g := range groups {
		if g.KeyPair.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

func FindGroupByName(groups []Group, name string) (Group, bool) {
	for _, g := range groups {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}
