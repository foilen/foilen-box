package model

// Script is a named, fixed shell command a peer offers to run on request
// from other peers/groups it has granted ActionRunScript to. There is no
// support for caller-supplied arguments — the command and its args are
// entirely owner-defined; the caller only gets to trigger it.
type Script struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

// ScriptRunRequest is sent by the caller over RunScriptProtocolID to ask a
// peer to run one of its own scripts by name.
type ScriptRunRequest struct {
	RunID string `json:"runId"`
	Name  string `json:"name"`
}

// ScriptRunAck is the synchronous reply to a ScriptRunRequest: whether the
// script was actually started (permission granted, name found), not
// whether it succeeded — the outcome, if it ever arrives, comes later as a
// ScriptCompletion.
type ScriptRunAck struct {
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

// ScriptCompletion is a best-effort, unsigned push sent back to the peer
// that triggered a run once it finishes. Delivery isn't guaranteed — either
// side may be offline by the time it's sent — so callers must not assume
// silence means failure.
type ScriptCompletion struct {
	RunID      string `json:"runId"`
	ScriptName string `json:"scriptName"`
	ExitCode   int    `json:"exitCode"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}
