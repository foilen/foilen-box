package model

type Script struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

type ScriptRunRequest struct {
	RunID string `json:"runId"`
	Name  string `json:"name"`
}

type ScriptRunAck struct {
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

type ScriptCompletion struct {
	RunID      string `json:"runId"`
	ScriptName string `json:"scriptName"`
	ExitCode   int    `json:"exitCode"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}
