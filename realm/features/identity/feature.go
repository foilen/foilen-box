package identity

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"foilen-realm/features/keypairpush"
	"foilen-realm/model"
)

const (
	PushProtocolID = protocol.ID("/foilen-box/identity-push/1.0.0")

	FeatureName = "common/identity"

	ActionPush model.PermissionAction = FeatureName + "/push"
)

type Feature = keypairpush.Feature

func New(onReceive func(name string, kp model.KeyPair) error) *Feature {
	return keypairpush.New("identity", FeatureName, PushProtocolID, ActionPush, onReceive)
}
