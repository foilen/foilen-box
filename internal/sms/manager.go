package sms

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	realm "foilen-realm"
	realmmaps "foilen-realm/features/maps"
	realmmodel "foilen-realm/model"

	"foilen-box/internal/notify"
)

const pollInterval = 5 * time.Second

const notifyFreshnessWindow = 10 * time.Minute

type Manager struct {
	mapsFeature *realmmaps.Feature
	cfg         *Service
	localPeerID func() string
	localGroups func() []realmmodel.Group

	mu      sync.Mutex
	bridge  PlatformBridge
	baseURL string

	pollMu     sync.Mutex
	knownKeys  map[string]map[string]bool
	seededMaps map[string]bool
}

func NewManager(mapsFeature *realmmaps.Feature, cfg *Service, localPeerID func() string, localGroups func() []realmmodel.Group) *Manager {
	return &Manager{
		mapsFeature: mapsFeature,
		cfg:         cfg,
		localPeerID: localPeerID,
		localGroups: localGroups,
		knownKeys:   map[string]map[string]bool{},
		seededMaps:  map[string]bool{},
	}
}

func (m *Manager) SetBridge(bridge PlatformBridge) {
	m.mu.Lock()
	m.bridge = bridge
	m.mu.Unlock()
}

func (m *Manager) getBridge() PlatformBridge {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bridge
}

func (m *Manager) SetBaseURL(baseURL string) {
	m.mu.Lock()
	m.baseURL = baseURL
	m.mu.Unlock()
}

func (m *Manager) getBaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL
}

// Poll loop

func (m *Manager) Start() {
	go func() {
		m.pollOnce()
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.pollOnce()
		}
	}()
}

func mapKey(groupID, storeName string) string {
	return groupID + "|" + storeName
}

func (m *Manager) pollOnce() {
	cfg := m.cfg.Load()
	localID := m.localPeerID()
	if localID == "" {
		return
	}
	for _, s := range m.mapsFeature.ListSummaries() {
		if !IsSmsStore(s.StoreName) {
			continue
		}
		rm, encrypted, available := m.mapsFeature.GetMap(s.GroupID, s.StoreName)
		if encrypted && !available {
			continue
		}
		m.processStore(s.GroupID, s.StoreName, rm, localID, cfg)
	}
}

func (m *Manager) processStore(groupID, storeName string, rm realmmodel.RealmMap, localID string, cfg Config) {
	id := mapKey(groupID, storeName)

	m.pollMu.Lock()
	known := m.knownKeys[id]
	if known == nil {
		known = map[string]bool{}
		m.knownKeys[id] = known
	}
	firstPass := !m.seededMaps[id]
	m.seededMaps[id] = true
	m.pollMu.Unlock()

	isOwnStore := cfg.Enabled && cfg.GroupID == groupID && cfg.StoreName == storeName

	for key, entry := range rm.Entries {
		peerID, kind, ok := parseKey(key)
		if !ok {
			continue
		}

		if kind == kindCreate {
			if isOwnStore && peerID == localID {
				m.fulfillCreate(groupID, storeName, key, entry)
			}
			continue
		}
		if kind == kindEnabled {
			continue
		}

		m.pollMu.Lock()
		alreadyKnown := known[key]
		known[key] = true
		m.pollMu.Unlock()
		if alreadyKnown || firstPass || peerID == localID {
			continue
		}

		var msg SmsMessage
		if err := json.Unmarshal([]byte(entry.Value), &msg); err != nil {
			continue
		}
		if time.Since(time.UnixMilli(msg.TimestampUnixMillis)) > notifyFreshnessWindow {
			continue
		}
		m.notify(groupID, storeName, msg)
	}
}

func (m *Manager) fulfillCreate(groupID, storeName, key string, entry realmmodel.MapEntry) {
	var req SmsCreateRequest
	if err := json.Unmarshal([]byte(entry.Value), &req); err != nil {
		log.Printf("sms: dropping malformed create-request %s/%s %q: %v", realmmodel.GroupLabel(m.localGroups(), groupID), storeName, key, err)
		_ = m.mapsFeature.DeleteValue(groupID, storeName, key)
		return
	}

	bridge := m.getBridge()
	if bridge == nil {
		return
	}
	if err := bridge.SendSms(req.PhoneNumber, req.Body); err != nil {
		log.Printf("sms: failed to send to %s: %v", req.PhoneNumber, err)
		return
	}

	localID := m.localPeerID()
	ts := time.Now().UnixMilli()
	msg := SmsMessage{
		PhoneNumber:         req.PhoneNumber,
		Direction:           DirectionOutgoing,
		Body:                req.Body,
		Sender:              localID,
		Receiver:            req.PhoneNumber,
		TimestampUnixMillis: ts,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := m.mapsFeature.SetValue(groupID, storeName, messageKey(localID, ts, data), string(data)); err != nil {
		log.Printf("sms: failed to record sent message: %v", err)
		return
	}
	_ = m.mapsFeature.DeleteValue(groupID, storeName, key)
}

// Device history

func (m *Manager) ImportHistory(groupID, storeName string) error {
	return m.reconcileDeviceStore(groupID, storeName)
}

func (m *Manager) reconcileDeviceStore(groupID, storeName string) error {
	bridge := m.getBridge()
	if bridge == nil {
		return fmt.Errorf("sms: no platform bridge available (desktop?)")
	}
	localID := m.localPeerID()
	if localID == "" {
		return fmt.Errorf("sms: local peer id not available yet")
	}

	raw, err := bridge.ReadAllSms()
	if err != nil {
		return fmt.Errorf("failed to read device SMS history: %w", err)
	}
	var rows []SmsMessage
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return fmt.Errorf("failed to parse device SMS history: %w", err)
	}

	rm, encrypted, available := m.mapsFeature.GetMap(groupID, storeName)
	if encrypted && !available {
		return fmt.Errorf("sms: store %s/%s isn't currently decryptable", groupID, storeName)
	}

	existingOwn := map[string]bool{}
	for key, entry := range rm.Entries {
		if entry.Deleted {
			continue
		}
		peerID, kind, ok := parseKey(key)
		if !ok || kind != "" || peerID != localID {
			continue
		}
		existingOwn[key] = true
	}

	onDevice := make(map[string]bool, len(rows))
	for _, msg := range rows {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		key := messageKey(localID, msg.TimestampUnixMillis, data)
		onDevice[key] = true
		if existingOwn[key] {
			continue
		}
		if err := m.mapsFeature.SetValue(groupID, storeName, key, string(data)); err != nil {
			log.Printf("sms: failed to reconcile-add %s: %v", key, err)
		}
	}

	for key := range existingOwn {
		if onDevice[key] {
			continue
		}
		if err := m.mapsFeature.DeleteValue(groupID, storeName, key); err != nil {
			log.Printf("sms: failed to reconcile-remove %s: %v", key, err)
		}
	}

	return nil
}

// realm.Feature

func (m *Manager) Name() string { return "box/sms" }

func (m *Manager) Actions() []realmmodel.PermissionAction { return nil }

func (m *Manager) RegisterHandlers(reg *realm.Registrar) {}

func (m *Manager) RunPeriodic(reg *realm.Registrar) {
	cfg := m.cfg.Load()
	if !cfg.Enabled || cfg.GroupID == "" || cfg.StoreName == "" {
		return
	}
	if m.getBridge() == nil {
		return
	}
	if err := m.reconcileDeviceStore(cfg.GroupID, cfg.StoreName); err != nil {
		log.Printf("sms: periodic reconcile failed: %v", err)
	}
	m.fulfillPendingCreates(cfg.GroupID, cfg.StoreName)
	m.touchEnabledMarker(cfg.GroupID, cfg.StoreName)
}

// Enabled marker

const enabledMarkerValue = "1"

func (m *Manager) touchEnabledMarker(groupID, storeName string) {
	localID := m.localPeerID()
	if localID == "" {
		return
	}
	if err := m.mapsFeature.SetValue(groupID, storeName, enabledKey(localID), enabledMarkerValue); err != nil {
		log.Printf("sms: failed to refresh enabled marker in %s/%s: %v", realmmodel.GroupLabel(m.localGroups(), groupID), storeName, err)
	}
}

func (m *Manager) clearEnabledMarker(groupID, storeName string) {
	localID := m.localPeerID()
	if localID == "" || groupID == "" || storeName == "" {
		return
	}
	if err := m.mapsFeature.DeleteValue(groupID, storeName, enabledKey(localID)); err != nil {
		log.Printf("sms: failed to clear enabled marker in %s/%s: %v", realmmodel.GroupLabel(m.localGroups(), groupID), storeName, err)
	}
}

func (m *Manager) SyncEnabledMarker(previous, newCfg Config) {
	if previous.Enabled && (previous.GroupID != newCfg.GroupID || previous.StoreName != newCfg.StoreName || !newCfg.Enabled) {
		m.clearEnabledMarker(previous.GroupID, previous.StoreName)
	}
	if newCfg.Enabled {
		m.touchEnabledMarker(newCfg.GroupID, newCfg.StoreName)
	}
}

func (m *Manager) EnabledPeerIDs(groupID, storeName string) []string {
	rm, encrypted, available := m.mapsFeature.GetMap(groupID, storeName)
	if encrypted && !available {
		return nil
	}
	var ids []string
	for key, entry := range rm.Entries {
		if entry.Deleted {
			continue
		}
		peerID, kind, ok := parseKey(key)
		if !ok || kind != kindEnabled {
			continue
		}
		ids = append(ids, peerID)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) fulfillPendingCreates(groupID, storeName string) {
	localID := m.localPeerID()
	if localID == "" {
		return
	}
	rm, encrypted, available := m.mapsFeature.GetMap(groupID, storeName)
	if encrypted && !available {
		return
	}
	for key, entry := range rm.Entries {
		if entry.Deleted {
			continue
		}
		peerID, kind, ok := parseKey(key)
		if !ok || kind != kindCreate || peerID != localID {
			continue
		}
		m.fulfillCreate(groupID, storeName, key, entry)
	}
}

// Messages API

func (m *Manager) HandleIncomingSms(sender, body string, timestampMillis int64) error {
	cfg := m.cfg.Load()
	if !cfg.Enabled {
		return nil
	}
	localID := m.localPeerID()
	msg := SmsMessage{
		PhoneNumber:         sender,
		Direction:           DirectionIncoming,
		Body:                body,
		Sender:              sender,
		Receiver:            localID,
		TimestampUnixMillis: timestampMillis,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return m.mapsFeature.SetValue(cfg.GroupID, cfg.StoreName, messageKey(localID, timestampMillis, data), string(data))
}

func (m *Manager) RequestSend(groupID, storeName, targetPeerID, phoneNumber, body string) error {
	if groupID == "" || storeName == "" || targetPeerID == "" || phoneNumber == "" || body == "" {
		return fmt.Errorf("groupId, storeName, peerId, phoneNumber, and body are required")
	}
	data, err := json.Marshal(SmsCreateRequest{PhoneNumber: phoneNumber, Body: body})
	if err != nil {
		return err
	}
	uid, err := randomHex()
	if err != nil {
		return err
	}
	return m.mapsFeature.SetValue(groupID, storeName, createKey(targetPeerID, uid), string(data))
}

func (m *Manager) ListConversations(groupID, storeName string) (summaries []ConversationSummary, encrypted, available bool) {
	messages, encrypted, available := m.listMessagesRaw(groupID, storeName, "")
	byPhone := map[string]*ConversationSummary{}
	for _, msg := range messages {
		c, ok := byPhone[msg.PhoneNumber]
		if !ok {
			c = &ConversationSummary{PhoneNumber: msg.PhoneNumber}
			byPhone[msg.PhoneNumber] = c
		}
		c.MessageCount++
		if msg.TimestampUnixMillis >= c.LastTimestampUnixMillis {
			c.LastTimestampUnixMillis = msg.TimestampUnixMillis
			c.LastMessageBody = msg.Body
			c.LastMessageDirection = msg.Direction
		}
	}
	for _, c := range byPhone {
		summaries = append(summaries, *c)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].LastTimestampUnixMillis > summaries[j].LastTimestampUnixMillis
	})
	return summaries, encrypted, available
}

func (m *Manager) ListMessages(groupID, storeName, phoneNumber string) (messages []SmsMessage, encrypted, available bool) {
	return m.listMessagesRaw(groupID, storeName, phoneNumber)
}

func (m *Manager) listMessagesRaw(groupID, storeName, phoneNumberFilter string) (messages []SmsMessage, encrypted, available bool) {
	rm, encrypted, available := m.mapsFeature.GetMap(groupID, storeName)
	if encrypted && !available {
		return nil, encrypted, available
	}
	for key, entry := range rm.Entries {
		if entry.Deleted {
			continue
		}
		peerID, kind, ok := parseKey(key)
		if !ok || kind != "" {
			continue
		}
		var msg SmsMessage
		if err := json.Unmarshal([]byte(entry.Value), &msg); err != nil {
			continue
		}
		if phoneNumberFilter != "" && msg.PhoneNumber != phoneNumberFilter {
			continue
		}
		msg.PeerID = peerID
		messages = append(messages, msg)
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].TimestampUnixMillis < messages[j].TimestampUnixMillis
	})
	return messages, encrypted, available
}

// Notifications

func (m *Manager) notify(groupID, storeName string, msg SmsMessage) {
	title := msg.Sender
	if title == "" {
		title = msg.PhoneNumber
	}
	preview := msg.Body
	if len(preview) > 20 {
		preview = preview[:20]
	}

	deepLink := groupID + "|" + storeName + "|" + msg.PhoneNumber

	if bridge := m.getBridge(); bridge != nil {
		bridge.ShowNotification(title, preview, deepLink)
		return
	}

	baseURL := m.getBaseURL()
	if baseURL == "" {
		if err := notify.Notify(title, preview); err != nil {
			log.Printf("sms: failed to show desktop notification: %v", err)
		}
		return
	}
	target := baseURL + "#realm/realm-sms-subtab/" + encodeURIComponent(deepLink)
	if err := notify.NotifyClick(title, preview, target); err != nil {
		log.Printf("sms: failed to show desktop notification: %v", err)
	}
}

func encodeURIComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
