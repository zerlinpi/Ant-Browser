// Package browseragent runs device-scoped cloud workflows through an injected
// local executor. It never accepts local profile paths or launch URLs from cloud.
package browseragent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Execution struct {
	TaskID     string
	InstanceID string
	Definition json.RawMessage
}
type Executor interface {
	Execute(context.Context, Execution) error
}
type Client struct {
	base        string
	deviceID    string
	workspaceID string
	credential  string
	http        *http.Client
	executor    Executor
}

type reportHTTPError struct{ status int }

type cloudTransportError struct{}

func (cloudTransportError) Error() string { return "cloud request failed" }

type retryableClaimError struct{ cause error }

func (e retryableClaimError) Error() string { return e.cause.Error() }
func (e retryableClaimError) Unwrap() error { return e.cause }

func (e reportHTTPError) Error() string {
	return fmt.Sprintf("execution report rejected: HTTP %d", e.status)
}

type Config struct{ BaseURL, DeviceID, WorkspaceID, Credential, AgentVersion string }

func New(config Config, executor Executor) (*Client, error) {
	config, err := validateConnectionConfig(config)
	if err != nil {
		return nil, err
	}
	if executor == nil {
		return nil, errors.New("local executor is required")
	}
	return &Client{base: config.BaseURL, deviceID: config.DeviceID, workspaceID: config.WorkspaceID, credential: config.Credential, executor: executor, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func validateConnectionConfig(config Config) (Config, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, errors.New("cloud endpoint must be HTTPS without credentials or query")
	}
	deviceID, err := uuid.Parse(strings.TrimSpace(config.DeviceID))
	if err != nil {
		return Config{}, errors.New("device and workspace IDs must be UUIDs")
	}
	workspaceID, err := uuid.Parse(strings.TrimSpace(config.WorkspaceID))
	if err != nil {
		return Config{}, errors.New("device and workspace IDs must be UUIDs")
	}
	if config.Credential == "" || strings.ContainsAny(config.Credential, "\r\n") {
		return Config{}, errors.New("device credentials are required")
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.DeviceID, config.WorkspaceID = deviceID.String(), workspaceID.String()
	config.AgentVersion = strings.TrimSpace(config.AgentVersion)
	if config.AgentVersion == "" {
		config.AgentVersion = "development"
	}
	if len(config.AgentVersion) > 64 || strings.ContainsAny(config.AgentVersion, "\r\n") {
		return Config{}, errors.New("agent version is invalid")
	}
	return config, nil
}

type definition struct {
	SchemaVersion string `json:"schemaVersion"`
	Engine        string `json:"engine"`
	Steps         []struct {
		ID              string                 `json:"id"`
		Action          string                 `json:"action"`
		TimeoutMS       int                    `json:"timeoutMs,omitempty"`
		ContinueOnError bool                   `json:"continueOnError,omitempty"`
		Parameters      map[string]interface{} `json:"parameters,omitempty"`
	} `json:"steps"`
}
type claimedExecution struct {
	Lease struct {
		Task struct {
			ID                string `json:"id"`
			WorkspaceID       string `json:"workspaceId"`
			TaskType          string `json:"taskType"`
			WorkflowID        string `json:"workflowId"`
			WorkflowVersionID string `json:"workflowVersionId"`
			LeaseOwner        string `json:"leaseOwner"`
			Payload           struct {
				InstanceID string `json:"instanceId"`
			} `json:"payload"`
		} `json:"task"`
		RunID     string    `json:"runId"`
		AttemptID string    `json:"attemptId"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"lease"`
	Version struct {
		ID          string     `json:"id"`
		WorkspaceID string     `json:"workspaceId"`
		WorkflowID  string     `json:"workflowId"`
		ContentHash string     `json:"contentHash"`
		Definition  definition `json:"definition"`
	} `json:"version"`
}

// PollOnce performs at most one execution. A report failure is returned to the
// caller; it must not rerun the browser operation to retry delivery of a report.
func (c *Client) PollOnce(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, status, err := c.post(ctx, "/api/v1/agent/tasks/claim", nil)
	if err != nil {
		var transportErr cloudTransportError
		if errors.As(err, &transportErr) {
			return false, retryableClaimError{cause: err}
		}
		return false, err
	}
	if status == http.StatusNoContent {
		return false, nil
	}
	if status != http.StatusOK {
		err := fmt.Errorf("claim rejected: HTTP %d", status)
		if status == http.StatusTooManyRequests || status >= 500 {
			return false, retryableClaimError{cause: err}
		}
		return false, err
	}
	var envelope struct {
		Data claimedExecution `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return false, errors.New("invalid execution response")
	}
	claim := envelope.Data
	task := claim.Lease.Task
	if task.WorkspaceID != c.workspaceID || task.LeaseOwner != "device:"+c.deviceID || task.TaskType != "workflow.execute" || claim.Version.WorkspaceID != c.workspaceID || claim.Version.WorkflowID != task.WorkflowID || claim.Version.ID != task.WorkflowVersionID {
		return false, errors.New("execution scope mismatch")
	}
	for _, id := range []string{task.ID, task.Payload.InstanceID, task.WorkflowID, task.WorkflowVersionID, claim.Lease.RunID, claim.Lease.AttemptID} {
		if _, err := uuid.Parse(id); err != nil {
			return false, errors.New("invalid execution identity")
		}
	}
	canonical, err := json.Marshal(claim.Version.Definition)
	if err != nil {
		return false, errors.New("invalid workflow definition")
	}
	hash := sha256.Sum256(canonical)
	engine := claim.Version.Definition.Engine
	if hex.EncodeToString(hash[:]) != claim.Version.ContentHash || (engine != "playwright" && engine != "puppeteer" && engine != "cdp") || claim.Version.Definition.SchemaVersion != "ant-workflow/v1" || len(claim.Version.Definition.Steps) < 1 || len(claim.Version.Definition.Steps) > 500 {
		return false, errors.New("workflow integrity or engine mismatch")
	}
	deadline := claim.Lease.ExpiresAt.Add(-10 * time.Second)
	if !deadline.After(time.Now()) {
		return false, errors.New("execution lease expired")
	}
	startReportCtx, startReportCancel := context.WithTimeout(ctx, 10*time.Second)
	err = c.reportWithRetry(startReportCtx, claim, "running")
	startReportCancel()
	if err != nil {
		return false, err
	}
	executionCtx, cancel := context.WithDeadline(ctx, deadline)
	executionErr := c.executor.Execute(executionCtx, Execution{TaskID: task.ID, InstanceID: task.Payload.InstanceID, Definition: canonical})
	if executionErr == nil {
		executionErr = executionCtx.Err()
	}
	cancel()
	state := "succeeded"
	if executionErr != nil {
		state = "failed"
	}
	// Give completion delivery a bounded chance even during application shutdown.
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer reportCancel()
	if err := c.reportWithRetry(reportCtx, claim, state); err != nil {
		return true, err
	}
	return true, nil
}

func (c *Client) reportWithRetry(ctx context.Context, claim claimedExecution, status string) error {
	delay := 100 * time.Millisecond
	for {
		err := c.report(ctx, claim, status)
		if err == nil {
			return nil
		}
		var httpErr reportHTTPError
		if errors.As(err, &httpErr) && httpErr.status != http.StatusTooManyRequests && httpErr.status < 500 {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < time.Second {
			delay *= 2
		}
	}
}

// Run polls serially and stops on any protocol/report error. An application may
// reconnect after inspecting the error, but must not rerun a completed operation
// merely because its report failed. No work is started after cancellation.
func (c *Client) Run(ctx context.Context, interval time.Duration) error {
	if interval < time.Second || interval > time.Minute {
		return errors.New("poll interval must be between one second and one minute")
	}
	retryDelay := 250 * time.Millisecond
	for {
		_, err := c.PollOnce(ctx)
		if err != nil {
			var retryable retryableClaimError
			if !errors.As(err, &retryable) {
				return err
			}
			if err := waitForPoll(ctx, retryDelay); err != nil {
				return err
			}
			if retryDelay < 5*time.Second {
				retryDelay *= 2
				if retryDelay > 5*time.Second {
					retryDelay = 5 * time.Second
				}
			}
			continue
		}
		retryDelay = 250 * time.Millisecond
		if err := waitForPoll(ctx, interval); err != nil {
			return err
		}
	}
}

func waitForPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) report(ctx context.Context, claim claimedExecution, status string) error {
	payload, _ := json.Marshal(map[string]string{"runId": claim.Lease.RunID, "attemptId": claim.Lease.AttemptID, "status": status})
	_, code, err := c.post(ctx, "/api/v1/agent/tasks/"+claim.Lease.Task.ID+"/report", payload)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return reportHTTPError{status: code}
	}
	return nil
}
func (c *Client) post(ctx context.Context, path string, payload []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, errors.New("invalid cloud request")
	}
	request.Header.Set("Authorization", "Device "+c.credential)
	request.Header.Set("X-Device-ID", c.deviceID)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, cloudTransportError{}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, 0, errors.New("cloud response invalid or oversized")
	}
	return data, response.StatusCode, nil
}
