package model

type Identity struct {
	Name    string  `json:"name"`
	KeyPair KeyPair `json:"keyPair"`
}

func FindIdentityByID(identities []Identity, id string) (Identity, bool) {
	for _, i := range identities {
		if i.KeyPair.ID == id {
			return i, true
		}
	}
	return Identity{}, false
}

func FindIdentityByName(identities []Identity, name string) (Identity, bool) {
	for _, i := range identities {
		if i.Name == name {
			return i, true
		}
	}
	return Identity{}, false
}
