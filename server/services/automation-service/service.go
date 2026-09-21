package automationservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound        = errors.New("workflow not found")
	ErrVersionConflict = errors.New("workflow version conflict")
	ErrStateConflict   = errors.New("workflow state conflict")
)

const (
	schemaVersion      = "ant-workflow/v1"
	maxSteps           = 500
	maxDefinitionBytes = 512 << 10
	maxStepTimeoutMS   = 10 * 60 * 1000
)

var stepIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type Workflow struct {
	ID                 string     `json:"id"`
	WorkspaceID        string     `json:"workspaceId"`
	Name               string     `json:"name"`
	Status             string     `json:"status"`
	LatestVersion      int        `json:"latestVersion"`
	PublishedVersionID string     `json:"publishedVersionId,omitempty"`
	Version            int64      `json:"version"`
	CreatedBy          string     `json:"createdBy,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	ArchivedAt         *time.Time `json:"archivedAt,omitempty"`
}

type WorkflowVersion struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspaceId"`
	WorkflowID    string     `json:"workflowId"`
	Version       int        `json:"version"`
	SchemaVersion string     `json:"schemaVersion"`
	Definition    Definition `json:"definition"`
	ContentHash   string     `json:"contentHash"`
	CreatedBy     string     `json:"createdBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

type Definition struct {
	SchemaVersion string `json:"schemaVersion"`
	Engine        string `json:"engine"`
	Steps         []Step `json:"steps"`
}

type Step struct {
	ID              string                 `json:"id"`
	Action          string                 `json:"action"`
	TimeoutMS       int                    `json:"timeoutMs,omitempty"`
	ContinueOnError bool                   `json:"continueOnError,omitempty"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
}

type CreateInput struct {
	Name       string     `json:"name"`
	Definition Definition `json:"definition"`
}

type AddVersionInput struct {
	ExpectedVersion int64      `json:"expectedVersion"`
	Definition      Definition `json:"definition"`
}

type StateInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
	WorkflowVersion int   `json:"workflowVersion,omitempty"`
}

type Repository interface {
	CreateWorkflow(context.Context, Workflow, WorkflowVersion) error
	FindWorkflow(context.Context, string, string) (Workflow, error)
	ListWorkflows(context.Context, string) ([]Workflow, error)
	AddWorkflowVersion(context.Context, WorkflowVersion, int64, time.Time) (Workflow, error)
	FindWorkflowVersion(context.Context, string, string, int) (WorkflowVersion, error)
	TransitionWorkflow(context.Context, string, string, string, int, int64, time.Time) (Workflow, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now}
}

func (s *Service) Create(ctx context.Context, actorID, workspaceID string, input CreateInput) (Workflow, WorkflowVersion, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowManage); err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return Workflow{}, WorkflowVersion{}, errors.New("valid workflow name is required")
	}
	definition, contentHash, err := normalizeDefinition(input.Definition)
	if err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	now := s.now().UTC()
	workflow := Workflow{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Name: name, Status: "draft",
		LatestVersion: 1, Version: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	version := WorkflowVersion{
		ID: uuid.NewString(), WorkspaceID: workspaceID, WorkflowID: workflow.ID,
		Version: 1, SchemaVersion: definition.SchemaVersion, Definition: definition,
		ContentHash: contentHash, CreatedBy: actorID, CreatedAt: now,
	}
	if err := s.repository.CreateWorkflow(ctx, workflow, version); err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	return workflow, version, nil
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]Workflow, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
		return nil, err
	}
	return s.repository.ListWorkflows(ctx, workspaceID)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, workflowID string) (Workflow, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
		return Workflow{}, err
	}
	return s.repository.FindWorkflow(ctx, workspaceID, workflowID)
}

func (s *Service) Version(ctx context.Context, actorID, workspaceID, workflowID string, version int) (WorkflowVersion, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
		return WorkflowVersion{}, err
	}
	if version < 1 {
		return WorkflowVersion{}, errors.New("workflow version must be positive")
	}
	return s.repository.FindWorkflowVersion(ctx, workspaceID, workflowID, version)
}

func (s *Service) AddVersion(ctx context.Context, actorID, workspaceID, workflowID string, input AddVersionInput) (Workflow, WorkflowVersion, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowManage); err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	if input.ExpectedVersion < 1 {
		return Workflow{}, WorkflowVersion{}, errors.New("expectedVersion must be positive")
	}
	workflow, err := s.repository.FindWorkflow(ctx, workspaceID, workflowID)
	if err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	if workflow.Status == "archived" {
		return Workflow{}, WorkflowVersion{}, ErrStateConflict
	}
	definition, contentHash, err := normalizeDefinition(input.Definition)
	if err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	now := s.now().UTC()
	version := WorkflowVersion{
		ID: uuid.NewString(), WorkspaceID: workspaceID, WorkflowID: workflowID,
		Version: workflow.LatestVersion + 1, SchemaVersion: definition.SchemaVersion,
		Definition: definition, ContentHash: contentHash, CreatedBy: actorID, CreatedAt: now,
	}
	workflow, err = s.repository.AddWorkflowVersion(ctx, version, input.ExpectedVersion, now)
	if err != nil {
		return Workflow{}, WorkflowVersion{}, err
	}
	return workflow, version, nil
}

func (s *Service) Publish(ctx context.Context, actorID, workspaceID, workflowID string, input StateInput) (Workflow, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowManage); err != nil {
		return Workflow{}, err
	}
	if input.ExpectedVersion < 1 || input.WorkflowVersion < 1 {
		return Workflow{}, errors.New("expectedVersion and workflowVersion must be positive")
	}
	if _, err := s.repository.FindWorkflowVersion(ctx, workspaceID, workflowID, input.WorkflowVersion); err != nil {
		return Workflow{}, err
	}
	return s.repository.TransitionWorkflow(
		ctx, workspaceID, workflowID, "published", input.WorkflowVersion,
		input.ExpectedVersion, s.now().UTC(),
	)
}

func (s *Service) Archive(ctx context.Context, actorID, workspaceID, workflowID string, input StateInput) (Workflow, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowManage); err != nil {
		return Workflow{}, err
	}
	if input.ExpectedVersion < 1 {
		return Workflow{}, errors.New("expectedVersion must be positive")
	}
	return s.repository.TransitionWorkflow(
		ctx, workspaceID, workflowID, "archived", 0,
		input.ExpectedVersion, s.now().UTC(),
	)
}

func normalizeDefinition(input Definition) (Definition, string, error) {
	input.SchemaVersion = strings.TrimSpace(input.SchemaVersion)
	if input.SchemaVersion == "" {
		input.SchemaVersion = schemaVersion
	}
	if input.SchemaVersion != schemaVersion {
		return Definition{}, "", fmt.Errorf("unsupported workflow schemaVersion %q", input.SchemaVersion)
	}
	input.Engine = strings.ToLower(strings.TrimSpace(input.Engine))
	switch input.Engine {
	case "playwright", "puppeteer", "cdp":
	default:
		return Definition{}, "", errors.New("workflow engine must be playwright, puppeteer, or cdp")
	}
	if len(input.Steps) == 0 || len(input.Steps) > maxSteps {
		return Definition{}, "", fmt.Errorf("workflow must contain between 1 and %d steps", maxSteps)
	}
	seen := make(map[string]struct{}, len(input.Steps))
	for index := range input.Steps {
		step := &input.Steps[index]
		step.ID = strings.TrimSpace(step.ID)
		step.Action = strings.ToLower(strings.TrimSpace(step.Action))
		if !stepIDPattern.MatchString(step.ID) {
			return Definition{}, "", fmt.Errorf("workflow step %d has an invalid id", index+1)
		}
		if _, exists := seen[step.ID]; exists {
			return Definition{}, "", fmt.Errorf("workflow step id %q is duplicated", step.ID)
		}
		seen[step.ID] = struct{}{}
		if step.TimeoutMS == 0 {
			step.TimeoutMS = 30_000
		}
		if step.TimeoutMS < 100 || step.TimeoutMS > maxStepTimeoutMS {
			return Definition{}, "", fmt.Errorf("workflow step %q timeoutMs is outside 100..600000", step.ID)
		}
		parameters, err := normalizeParameters(step.Parameters)
		if err != nil {
			return Definition{}, "", fmt.Errorf("workflow step %q: %w", step.ID, err)
		}
		step.Parameters = parameters
		if err := validateStep(*step); err != nil {
			return Definition{}, "", fmt.Errorf("workflow step %q: %w", step.ID, err)
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return Definition{}, "", err
	}
	if len(encoded) > maxDefinitionBytes {
		return Definition{}, "", fmt.Errorf("workflow definition exceeds %d bytes", maxDefinitionBytes)
	}
	digest := sha256.Sum256(encoded)
	return input, hex.EncodeToString(digest[:]), nil
}

func normalizeParameters(input map[string]interface{}) (map[string]interface{}, error) {
	if input == nil {
		return map[string]interface{}{}, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("parameters must be JSON serializable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded map[string]interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("parameters must be a JSON object")
	}
	value, err := normalizeJSON(decoded)
	if err != nil {
		return nil, err
	}
	return value.(map[string]interface{}), nil
}

func normalizeJSON(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalizedKey := strings.TrimSpace(key)
			if normalizedKey == "" {
				return nil, errors.New("parameter keys cannot be empty")
			}
			lower := strings.ToLower(normalizedKey)
			if isSensitiveKey(lower) && !strings.HasSuffix(lower, "ref") {
				return nil, fmt.Errorf("parameter %q must use an external secret reference", normalizedKey)
			}
			normalized, err := normalizeJSON(typed[key])
			if err != nil {
				return nil, err
			}
			result[normalizedKey] = normalized
		}
		return result, nil
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index := range typed {
			normalized, err := normalizeJSON(typed[index])
			if err != nil {
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case json.Number:
		if integer, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
			return integer, nil
		}
		floating, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return nil, errors.New("parameter number is invalid")
		}
		return floating, nil
	case string, bool, nil:
		return typed, nil
	default:
		return nil, errors.New("parameters contain an unsupported JSON value")
	}
}

func isSensitiveKey(key string) bool {
	for _, fragment := range []string{"password", "passwd", "cookie", "secret", "token", "authorization", "totp"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func validateStep(step Step) error {
	allowed := map[string]map[string]struct{}{
		"navigate":   keySet("url", "waitUntil"),
		"click":      keySet("selector", "button"),
		"input":      keySet("selector", "value", "valueRef", "clear"),
		"wait":       keySet("durationMs", "selector", "state"),
		"upload":     keySet("selector", "artifactRef"),
		"javascript": keySet("script", "arguments"),
		"screenshot": keySet("name", "format", "fullPage"),
		"extract":    keySet("selector", "attribute", "storeAs", "multiple"),
		"close":      keySet(),
	}
	keys, ok := allowed[step.Action]
	if !ok {
		return errors.New("action must be navigate, click, input, wait, upload, javascript, screenshot, extract, or close")
	}
	for key := range step.Parameters {
		if _, exists := keys[key]; !exists {
			return fmt.Errorf("parameter %q is not supported for %s", key, step.Action)
		}
	}
	switch step.Action {
	case "navigate":
		raw, err := requireString(step.Parameters, "url", 4096)
		if err != nil {
			return err
		}
		if !strings.Contains(raw, "{{") {
			parsed, parseErr := url.Parse(raw)
			if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return errors.New("url must be an HTTP(S) URL without embedded credentials")
			}
		}
	case "click":
		_, err := requireString(step.Parameters, "selector", 2048)
		return err
	case "input":
		selector, err := requireString(step.Parameters, "selector", 2048)
		if err != nil {
			return err
		}
		_, hasValue := step.Parameters["value"]
		valueRef, hasRef := step.Parameters["valueRef"]
		if hasValue == hasRef {
			return errors.New("input requires exactly one of value or valueRef")
		}
		if strings.Contains(strings.ToLower(selector), "password") && hasValue {
			return errors.New("password inputs must be configured with valueRef")
		}
		if hasRef {
			if err := validateReference(valueRef, "valueRef"); err != nil {
				return err
			}
		}
	case "wait":
		_, duration := step.Parameters["durationMs"]
		_, selector := step.Parameters["selector"]
		if duration == selector {
			return errors.New("wait requires exactly one of durationMs or selector")
		}
		if duration {
			value, ok := integerParameter(step.Parameters["durationMs"])
			if !ok || value < 1 || value > 300_000 {
				return errors.New("durationMs must be between 1 and 300000")
			}
		}
	case "upload":
		if _, err := requireString(step.Parameters, "selector", 2048); err != nil {
			return err
		}
		return validateReference(step.Parameters["artifactRef"], "artifactRef")
	case "javascript":
		_, err := requireString(step.Parameters, "script", 64<<10)
		return err
	case "screenshot":
		if _, err := requireString(step.Parameters, "name", 120); err != nil {
			return err
		}
		if format, ok := step.Parameters["format"].(string); ok && format != "png" && format != "jpeg" {
			return errors.New("screenshot format must be png or jpeg")
		}
	case "extract":
		if _, err := requireString(step.Parameters, "selector", 2048); err != nil {
			return err
		}
		_, err := requireString(step.Parameters, "storeAs", 120)
		return err
	case "close":
		if len(step.Parameters) != 0 {
			return errors.New("close does not accept parameters")
		}
	}
	return nil
}

func requireString(parameters map[string]interface{}, key string, maximum int) (string, error) {
	value, ok := parameters[key].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" || len(value) > maximum {
		return "", fmt.Errorf("%s must be a non-empty string no longer than %d bytes", key, maximum)
	}
	parameters[key] = value
	return value, nil
}

func validateReference(value interface{}, name string) error {
	reference, ok := value.(string)
	reference = strings.TrimSpace(reference)
	if !ok || reference == "" || len(reference) > 512 {
		return fmt.Errorf("%s is required", name)
	}
	if !(strings.HasPrefix(reference, "account-secret://") || strings.HasPrefix(reference, "artifact://") || strings.HasPrefix(reference, "variable://")) {
		return fmt.Errorf("%s must be an account-secret, artifact, or variable reference", name)
	}
	return nil
}

func integerParameter(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case float64:
		integer := int64(typed)
		return integer, float64(integer) == typed
	default:
		return 0, false
	}
}

func keySet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
