package gatewayservice_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestFingerprintBatchAndPresetAPIContract(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "fingerprint-batch-owner@example.com")
	viewer := register(t, handler, "fingerprint-batch-viewer@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Fingerprint Batch Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	base := "/api/v1/workspaces/" + workspace.ID

	assertStatus(t, perform(t, handler, http.MethodGet, base+"/fingerprint-presets", "", "", nil), http.StatusUnauthorized)
	presetResponse := perform(t, handler, http.MethodGet, base+"/fingerprint-presets", owner.AccessToken, "", nil)
	assertStatus(t, presetResponse, http.StatusOK)
	presets := decodeData[[]fingerprintservice.Preset](t, presetResponse)
	if len(presets) != 3 || presets[0].Key != "amazon-us" || presets[1].Key != "tiktok-us" || presets[2].Key != "facebook-eu" {
		t.Fatalf("unexpected preset catalog: %+v", presets)
	}
	if presets[0].Input.Configuration.MediaDevices == nil || presets[0].Input.Configuration.Battery == nil ||
		len(presets[0].Input.Configuration.Fonts) == 0 || presets[0].Input.Configuration.WebGLRenderer == "" {
		t.Fatalf("preset omitted advanced configuration: %+v", presets[0])
	}

	batchBody := map[string]interface{}{
		"namePrefix": "Campaign", "count": 3, "mode": "fixed", "browserMajor": 144,
		"platform": "windows", "seedStart": "7000", "locale": "en-US", "timezone": "America/New_York",
		"configuration": map[string]interface{}{
			"windowWidth": 1440, "windowHeight": 900, "hardwareConcurrency": 8,
			"deviceMemory": 8, "colorDepth": 24, "maxTouchPoints": 0, "doNotTrack": "1",
			"screenWidth": 1920, "screenHeight": 1080, "deviceScaleFactor": 1,
			"webRTCPolicy": "disable_non_proxied_udp", "canvasNoise": true, "audioNoise": true, "clientRectsNoise": true,
			"fonts": []string{"Arial", "Segoe UI"}, "webGLVendor": "Google Inc. (Intel)", "webGLRenderer": "ANGLE (Intel, D3D11)",
			"mediaDevices": map[string]interface{}{"audioInputs": 1, "videoInputs": 1, "audioOutputs": 1},
			"battery":      map[string]interface{}{"charging": true, "level": 0.8, "chargingTimeSeconds": 600, "dischargingTimeSeconds": 7200},
		},
	}
	createdResponse := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", batchBody)
	assertStatus(t, createdResponse, http.StatusCreated)
	created := decodeData[[]fingerprintservice.Template](t, createdResponse)
	if len(created) != 3 {
		t.Fatalf("created batch size = %d, want 3", len(created))
	}
	for index, template := range created {
		expectedNames := []string{"Campaign 01", "Campaign 02", "Campaign 03"}
		if template.Name != expectedNames[index] || template.Seed != int64(7000+index) || template.Version != 1 || len(template.RuntimeArgs) == 0 {
			t.Fatalf("created template %d is invalid: %+v", index, template)
		}
	}

	// A collision anywhere in the generated range rejects the whole write.
	conflict := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", batchBody)
	assertStatus(t, conflict, http.StatusConflict)
	assertAPIErrorCode(t, conflict, "fingerprint_name_conflict")
	listedResponse := perform(t, handler, http.MethodGet, base+"/fingerprint-templates", owner.AccessToken, "", nil)
	assertStatus(t, listedResponse, http.StatusOK)
	if listed := decodeData[[]fingerprintservice.Template](t, listedResponse); len(listed) != 3 {
		t.Fatalf("conflicting batch changed stored templates: %+v", listed)
	}

	empty := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", map[string]interface{}{"count": 0})
	assertStatus(t, empty, http.StatusUnprocessableEntity)
	assertAPIErrorCode(t, empty, "fingerprint_validation_failed")

	tooLargeBody := copyStringInterfaceMap(batchBody)
	tooLargeBody["count"] = fingerprintservice.MaxBatchTemplates + 1
	tooLarge := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", tooLargeBody)
	assertStatus(t, tooLarge, http.StatusRequestEntityTooLarge)
	var tooLargeError struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]int `json:"details"`
		} `json:"error"`
	}
	decodeResponse(t, tooLarge, &tooLargeError)
	if tooLargeError.Error.Code != "fingerprint_batch_too_large" || tooLargeError.Error.Details["maxItems"] != fingerprintservice.MaxBatchTemplates {
		t.Fatalf("large fingerprint batch error = %+v", tooLargeError)
	}

	missingSeedBody := copyStringInterfaceMap(batchBody)
	delete(missingSeedBody, "seedStart")
	missingSeed := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", missingSeedBody)
	assertStatus(t, missingSeed, http.StatusUnprocessableEntity)
	assertAPIErrorCode(t, missingSeed, "fingerprint_validation_failed")

	invalidConfigurationBody := copyStringInterfaceMap(batchBody)
	invalidConfiguration := copyStringInterfaceMap(batchBody["configuration"].(map[string]interface{}))
	invalidConfiguration["deviceMemory"] = 3
	invalidConfigurationBody["configuration"] = invalidConfiguration
	invalid := perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", owner.AccessToken, "", invalidConfigurationBody)
	assertStatus(t, invalid, http.StatusUnprocessableEntity)
	assertAPIErrorCode(t, invalid, "fingerprint_validation_failed")

	memberResponse := perform(t, handler, http.MethodPost, base+"/members", owner.AccessToken, "", map[string]string{
		"userId": viewer.User.ID, "role": "viewer",
	})
	assertStatus(t, memberResponse, http.StatusCreated)
	assertStatus(t, perform(t, handler, http.MethodGet, base+"/fingerprint-presets", viewer.AccessToken, "", nil), http.StatusOK)
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/fingerprint-templates/batch", viewer.AccessToken, "", batchBody), http.StatusForbidden)
}

func assertAPIErrorCode(t *testing.T, response *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var problem struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, response, &problem)
	if problem.Error.Code != expected {
		t.Fatalf("API error code = %q, want %q; body=%s", problem.Error.Code, expected, response.Body.String())
	}
}

func copyStringInterfaceMap(source map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
