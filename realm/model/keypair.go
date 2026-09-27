package model

type KeyPair struct {
	ID               string `json:"id"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}
