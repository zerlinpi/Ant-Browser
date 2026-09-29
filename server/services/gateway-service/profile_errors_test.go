package gatewayservice

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

// Resource and schedule validation errors used to fall through to 500.
func TestResourceConflictAndScheduleErrorsMapToStableCodes(t *testing.T) {
	g := &Gateway{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, malformed := scheduleservice.ParseCron("* * *")
	_, emptyItem := scheduleservice.ParseCron("1,,2 * * * *")
	never, err := scheduleservice.ParseCron("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	_, neverFires := never.Next(time.Now(), time.UTC)
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{browserinstanceservice.ErrNameConflict, http.StatusConflict, "instance_name_conflict"},
		{proxyservice.ErrNameConflict, http.StatusConflict, "proxy_name_conflict"},
		{accountservice.ErrIdentifierConflict, http.StatusConflict, "account_identifier_conflict"},
		{profilesyncservice.ErrNameConflict, http.StatusConflict, "profile_name_conflict"},
		{automationservice.ErrNameConflict, http.StatusConflict, "workflow_name_conflict"},
		{proxyservice.ErrAssignmentConflict, http.StatusConflict, "proxy_assignment_conflict"},
		{malformed, http.StatusUnprocessableEntity, "invalid_cron_expression"},
		{emptyItem, http.StatusUnprocessableEntity, "invalid_cron_expression"},
		{neverFires, http.StatusUnprocessableEntity, "invalid_cron_expression"},
		{scheduleservice.ErrInvalidTimezone, http.StatusUnprocessableEntity, "invalid_timezone"},
	} {
		recorder := httptest.NewRecorder()
		g.writeServiceError(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/w/schedules", nil), test.err)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
			t.Fatalf("%v: decode error body: %v", test.err, err)
		}
		if recorder.Code != test.status || body.Error.Code != test.code {
			t.Errorf("%v: status=%d code=%q, want %d %q", test.err, recorder.Code, body.Error.Code, test.status, test.code)
		}
	}
}

// Desktop agents branch on these codes (for example to stop retrying while a
// conflict is open), so the mapping is part of the sync protocol.
func TestProfileSyncErrorsMapToStableCodes(t *testing.T) {
	g := &Gateway{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{profilesyncservice.ErrLeaseHeld, http.StatusLocked, "profile_lease_held"},
		{profilesyncservice.ErrLeaseInvalid, http.StatusConflict, "profile_lease_invalid"},
		{profilesyncservice.ErrRevisionConflict, http.StatusConflict, "profile_revision_conflict"},
		{profilesyncservice.ErrConflictUnresolved, http.StatusConflict, "profile_conflict_unresolved"},
		{profilesyncservice.ErrObjectUnavailable, http.StatusUnprocessableEntity, "profile_object_unavailable"},
		{fmt.Errorf("commit: %w", profilesyncservice.ErrStorageQuotaExceeded), http.StatusPaymentRequired, "quota_exceeded"},
	} {
		recorder := httptest.NewRecorder()
		g.writeServiceError(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/agent/profiles/p/revisions", nil), test.err)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
			t.Fatalf("%v: decode error body: %v", test.err, err)
		}
		if recorder.Code != test.status || body.Error.Code != test.code {
			t.Errorf("%v: status=%d code=%q, want %d %q", test.err, recorder.Code, body.Error.Code, test.status, test.code)
		}
	}
}
