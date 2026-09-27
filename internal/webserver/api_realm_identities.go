package webserver

import (
	"encoding/json"
	"fmt"
	"log"

	realmkeypair "foilen-realm/keypair"
	realmmodel "foilen-realm/model"
)

func (a *api) identityExists(name string) bool {
	_, ok := realmmodel.FindIdentityByName(a.realmConfig.Load().Identities, name)
	return ok
}

func (a *api) createIdentity(name string, kp realmmodel.KeyPair) (realmmodel.Config, error) {
	return a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Identities = append(c.Identities, realmmodel.Identity{Name: name, KeyPair: kp})
	})
}

func handleRealmAddIdentity(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, fmt.Errorf("please enter an identity name")
	}
	if a.identityExists(p.Name) {
		return nil, fmt.Errorf("an identity named %q already exists", p.Name)
	}
	kp, err := realmkeypair.Generate()
	if err != nil {
		return nil, err
	}
	cfg, err := a.createIdentity(p.Name, kp)
	if err != nil {
		return nil, err
	}
	log.Printf("realm identity: added identity %q", p.Name)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmImportIdentity(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name             string `json:"name"`
		PrivateKeyBase64 string `json:"privateKeyBase64"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.PrivateKeyBase64 == "" {
		return nil, fmt.Errorf("please enter both an identity name and the private key")
	}
	if a.identityExists(p.Name) {
		return nil, fmt.Errorf("an identity named %q already exists", p.Name)
	}
	kp, err := realmkeypair.Import(p.PrivateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	cfg, err := a.createIdentity(p.Name, kp)
	if err != nil {
		return nil, err
	}
	log.Printf("realm identity: imported identity %q", p.Name)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmDeleteIdentity(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		filtered := c.Identities[:0]
		for _, id := range c.Identities {
			if id.Name != p.Name {
				filtered = append(filtered, id)
			}
		}
		c.Identities = filtered
	})
	if err != nil {
		return nil, err
	}
	log.Printf("realm identity: deleted identity %q", p.Name)
	return realmConfigResponse(a, cfg), nil
}

type exportIdentityResult struct {
	Name             string `json:"name"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

func handleRealmExportIdentity(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	identity, ok := realmmodel.FindIdentityByName(a.realmConfig.Load().Identities, p.Name)
	if !ok {
		return nil, fmt.Errorf("no identity named %q", p.Name)
	}
	log.Printf("realm identity: exported identity %q", p.Name)
	return exportIdentityResult{Name: identity.Name, PrivateKeyBase64: identity.KeyPair.PrivateKeyBase64}, nil
}

func handleRealmPushIdentity(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name   string `json:"name"`
		PeerId string `json:"peerId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.PeerId == "" {
		return nil, fmt.Errorf("please select both an identity and a peer")
	}
	identity, ok := realmmodel.FindIdentityByName(a.realmConfig.Load().Identities, p.Name)
	if !ok {
		return nil, fmt.Errorf("no identity named %q", p.Name)
	}
	if err := a.realmIdentity.Push(p.PeerId, p.Name, identity.KeyPair); err != nil {
		return nil, err
	}
	return map[string]any{"pushed": true}, nil
}
