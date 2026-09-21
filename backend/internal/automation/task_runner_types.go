package automation

import (
	"encoding/json"
	"time"
)

// WorkflowTaskRequest reuses the existing local launch, task cancellation and
// artifact lifecycle. Definition must be the authorized immutable cloud version.
type WorkflowTaskRequest struct {
	ScriptTaskRequest
	Definition json.RawMessage `json:"definition"`
}

type ScriptTaskRequest struct {
	TaskKey          string         `json:"taskKey"`
	ScriptPath       string         `json:"scriptPath"`
	Selector         map[string]any `json:"selector,omitempty"`
	Params           map[string]any `json:"params,omitempty"`
	LaunchBaseURL    string         `json:"launchBaseUrl"`
	LaunchAuthHeader string         `json:"launchAuthHeader,omitempty"`
	LaunchAuthValue  string         `json:"launchAuthValue,omitempty"`
	ArtifactDir      string         `json:"artifactDir,omitempty"`
	Timeout          time.Duration  `json:"-"`
}

type ScriptTaskResult struct {
	TaskID            string `json:"taskId"`
	TaskKey           string `json:"taskKey"`
	OK                bool   `json:"ok"`
	Summary           string `json:"summary"`
	Error             string `json:"error"`
	ResultText        string `json:"resultText"`
	LogText           string `json:"logText"`
	DurationMs        int64  `json:"durationMs"`
	StartedAt         string `json:"startedAt"`
	FinishedAt        string `json:"finishedAt"`
	RuntimeVersion    string `json:"runtimeVersion"`
	NodeVersion       string `json:"nodeVersion"`
	PlaywrightVersion string `json:"playwrightVersion"`
	PuppeteerVersion  string `json:"puppeteerVersion"`
}

type taskRunnerPayload struct {
	Workflow         json.RawMessage `json:"workflow,omitempty"`
	TaskType         string          `json:"taskType,omitempty"`
	RuntimeDir       string          `json:"runtimeDir"`
	ScriptPath       string          `json:"scriptPath,omitempty"`
	Selector         map[string]any  `json:"selector,omitempty"`
	Params           map[string]any  `json:"params,omitempty"`
	LaunchBaseURL    string          `json:"launchBaseUrl,omitempty"`
	LaunchAuthHeader string          `json:"launchAuthHeader,omitempty"`
	LaunchAuthValue  string          `json:"launchAuthValue,omitempty"`
	ArtifactDir      string          `json:"artifactDir,omitempty"`
}

type taskRunnerResponse struct {
	OK             bool                 `json:"ok"`
	Summary        string               `json:"summary,omitempty"`
	Error          string               `json:"error,omitempty"`
	Title          string               `json:"title"`
	URL            string               `json:"url"`
	ScreenshotPath string               `json:"screenshotPath,omitempty"`
	StartedAt      string               `json:"startedAt"`
	FinishedAt     string               `json:"finishedAt"`
	IsolatedPage   bool                 `json:"isolatedPage"`
	Logs           []taskRunnerLogEntry `json:"logs,omitempty"`
}

type taskRunnerLogEntry struct {
	Time   string `json:"time"`
	Values []any  `json:"values"`
}

type TaskEvent struct {
	TaskID     string `json:"taskId"`
	ProfileID  string `json:"profileId"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}
