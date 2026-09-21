package cloudagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	BaseURL     string            `json:"baseUrl"`
	DeviceID    string            `json:"deviceId"`
	WorkspaceID string            `json:"workspaceId"`
	Bindings    map[string]string `json:"bindings"`
}

// Credentials intentionally cannot be stored in this configuration file.
func LoadConfig(path string) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, errors.New("cloud agent config path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return Config{}, errors.New("cloud agent config is too large or unreadable")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, errors.New("invalid cloud agent config")
	}
	if decoder.Decode(new(interface{})) != io.EOF {
		return Config{}, errors.New("trailing cloud agent config data")
	}
	if len(config.Bindings) == 0 {
		return Config{}, errors.New("explicit instance bindings are required")
	}
	deviceID, err := uuid.Parse(strings.TrimSpace(config.DeviceID))
	if err != nil {
		return Config{}, errors.New("device ID must be a UUID")
	}
	workspaceID, err := uuid.Parse(strings.TrimSpace(config.WorkspaceID))
	if err != nil {
		return Config{}, errors.New("workspace ID must be a UUID")
	}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.DeviceID = deviceID.String()
	config.WorkspaceID = workspaceID.String()
	bindings := make(map[string]string, len(config.Bindings))
	seen := map[string]bool{}
	for cloudID, profileID := range config.Bindings {
		parsedCloudID, err := uuid.Parse(strings.TrimSpace(cloudID))
		if err != nil {
			return Config{}, errors.New("cloud binding IDs must be UUIDs")
		}
		canonicalCloudID := parsedCloudID.String()
		profileID = strings.TrimSpace(profileID)
		if profileID == "" || seen[profileID] {
			return Config{}, errors.New("bindings must select distinct local profiles")
		}
		if _, duplicate := bindings[canonicalCloudID]; duplicate {
			return Config{}, errors.New("cloud binding IDs must be distinct")
		}
		seen[profileID] = true
		bindings[canonicalCloudID] = profileID
	}
	config.Bindings = bindings
	return config, nil
}
