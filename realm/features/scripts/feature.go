package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	"foilen-realm/model"
)

const (
	RunProtocolID        = protocol.ID("/foilen-box/scripts-run/1.0.0")
	CompletionProtocolID = protocol.ID("/foilen-box/scripts-completion/1.0.0")
	ioTimeout            = 10 * time.Second
	maxBytes             = 16 * 1024
	runRetention         = time.Hour

	FeatureName = "common/scripts"

	ActionRun model.PermissionAction = FeatureName + "/run"
)

type Run struct {
	RunID      string
	PeerID     string
	ScriptName string
	StartedAt  time.Time
	Status     string
	ExitCode   int
	Error      string
}

const (
	RunStarted   = "started"
	RunCompleted = "completed"
	RunFailed    = "failed"
)

type Feature struct {
	mu  sync.Mutex
	reg *realm.Registrar

	runsMu sync.Mutex
	runs   map[string]*Run
}

func New() *Feature {
	return &Feature{runs: make(map[string]*Run)}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{ActionRun}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(RunProtocolID, f.handleRunStream(reg))
	reg.SetStreamHandler(CompletionProtocolID, f.handleCompletionStream())
}

func (f *Feature) RunScript(to, scriptName string) (string, error) {
	reg := f.registrar()
	if reg == nil {
		return "", fmt.Errorf("realm scripts: not registered on an engine")
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return "", fmt.Errorf("realm scripts: not running")
	}

	pid, err := peer.Decode(to)
	if err != nil {
		return "", fmt.Errorf("realm scripts: invalid peer id %q: %w", to, err)
	}
	if err := reg.EnsureConnected(ctx, pid); err != nil {
		return "", fmt.Errorf("realm scripts: peer %s unreachable to run script: %w", to, err)
	}

	runID := uuid.NewString()

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, RunProtocolID)
	if err != nil {
		return "", fmt.Errorf("realm scripts: peer %s unreachable to run script: %w", to, err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(model.ScriptRunRequest{RunID: runID, Name: scriptName}); err != nil {
		return "", fmt.Errorf("realm scripts: failed to send run request to %s: %w", to, err)
	}

	var ack model.ScriptRunAck
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&ack); err != nil {
		return "", fmt.Errorf("realm scripts: failed to read run ack from %s: %w", to, err)
	}
	if !ack.Started {
		return "", fmt.Errorf("realm scripts: %s refused to run %q: %s", to, scriptName, ack.Error)
	}

	f.putRun(&Run{RunID: runID, PeerID: to, ScriptName: scriptName, StartedAt: time.Now(), Status: RunStarted})
	return runID, nil
}

func (f *Feature) handleRunStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		remote := s.Conn().RemotePeer()

		var req model.ScriptRunRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm scripts: failed to decode script run request from %s: %v", reg.PeerLabel(remote.String()), err)
			return
		}

		if !reg.IsAllowed(remote, ActionRun) {
			log.Printf("realm scripts: script run request from %s rejected: no permission", reg.PeerLabel(remote.String()))
			_ = json.NewEncoder(s).Encode(model.ScriptRunAck{Started: false, Error: "not allowed"})
			return
		}

		cfg := reg.Config()
		var script *model.Script
		for i := range cfg.Scripts {
			if cfg.Scripts[i].Name == req.Name {
				script = &cfg.Scripts[i]
				break
			}
		}

		if script == nil {
			_ = json.NewEncoder(s).Encode(model.ScriptRunAck{Started: false, Error: "no such script"})
			return
		}

		if err := json.NewEncoder(s).Encode(model.ScriptRunAck{Started: true}); err != nil {
			log.Printf("realm scripts: failed to ack script run to %s: %v", reg.PeerLabel(remote.String()), err)
			return
		}

		h := reg.Host()
		ctx := reg.Context()
		command, args, workingDirectory := script.Command, script.Args, script.WorkingDirectory
		runID, scriptName := req.RunID, script.Name
		go func() {
			cmd := exec.CommandContext(context.Background(), command, args...)
			cmd.Dir = workingDirectory
			runErr := cmd.Run()
			exitCode := 0
			errMsg := ""
			if runErr != nil {
				errMsg = runErr.Error()
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = -1
				}
			}
			if h != nil && ctx != nil {
				sendCompletion(ctx, h, remote, reg.PeerLabel(remote.String()), model.ScriptCompletion{
					RunID:      runID,
					ScriptName: scriptName,
					ExitCode:   exitCode,
					Success:    runErr == nil,
					Error:      errMsg,
				})
			}
		}()
	}
}

func sendCompletion(ctx context.Context, h host.Host, to peer.ID, label string, completion model.ScriptCompletion) {
	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, to, CompletionProtocolID)
	if err != nil {
		log.Printf("realm scripts: peer %s unreachable to report script completion (dropped): %v", label, err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(completion); err != nil {
		log.Printf("realm scripts: failed to send script completion to %s: %v", label, err)
	}
}

func (f *Feature) handleCompletionStream() network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var completion model.ScriptCompletion
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&completion); err != nil {
			log.Printf("realm scripts: failed to decode script completion from %s: %v", f.registrar().PeerLabel(s.Conn().RemotePeer().String()), err)
			return
		}

		f.runsMu.Lock()
		defer f.runsMu.Unlock()
		run, ok := f.runs[completion.RunID]
		if !ok {
			return
		}
		if completion.Success {
			run.Status = RunCompleted
		} else {
			run.Status = RunFailed
		}
		run.ExitCode = completion.ExitCode
		run.Error = completion.Error
	}
}

func (f *Feature) putRun(run *Run) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	f.runs[run.RunID] = run
	cutoff := time.Now().Add(-runRetention)
	for id, r := range f.runs {
		if r.StartedAt.Before(cutoff) {
			delete(f.runs, id)
		}
	}
}

func (f *Feature) OnPeerRemoved(id string) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	for runID, r := range f.runs {
		if r.PeerID == id {
			delete(f.runs, runID)
		}
	}
}

func (f *Feature) ListRuns() []Run {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	result := make([]Run, 0, len(f.runs))
	for _, r := range f.runs {
		result = append(result, *r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.After(result[j].StartedAt) })
	return result
}
