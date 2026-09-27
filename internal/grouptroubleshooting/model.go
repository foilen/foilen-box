package grouptroubleshooting

const CommonStoreName = "common"

const keyPrefix = "groupTroubleshooting/"

const expirationKey = keyPrefix + "expiration"

const startKey = keyPrefix + "start"

const (
	connectionsKeyPrefix = keyPrefix + "peer/"
	connectionsKeySuffix = "/connections"
)

const (
	startedKeyPrefix = keyPrefix + "peer/"
	startedKeySuffix = "/started"
)

type Expiration struct {
	ExpiresAtUnixMillis int64 `json:"expiresAtUnixMillis"`
}

type Start struct {
	StartAtUnixMillis int64 `json:"startAtUnixMillis"`
}

type Started struct {
	StartAtUnixMillis   int64 `json:"startAtUnixMillis"`
	StartedAtUnixMillis int64 `json:"startedAtUnixMillis"`
}

type Connection struct {
	RemotePeerID string `json:"remotePeerId"`
	Address      string `json:"address"`
}

func connectionsKey(peerID string) string {
	return connectionsKeyPrefix + peerID + connectionsKeySuffix
}

func startedKey(peerID string) string {
	return startedKeyPrefix + peerID + startedKeySuffix
}
