package gatewayservice

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	analyticsservice "github.com/zerlinpi/Ant-Browser/server/services/analytics-service"
)

func (g *Gateway) analyticsDashboard(w http.ResponseWriter, r *http.Request) {
	window, err := analyticsWindow(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.analytics.Dashboard(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), window)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) analyticsEvents(w http.ResponseWriter, r *http.Request) {
	window, err := analyticsWindow(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, offset, err := analyticsPage(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.analytics.Events(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), analyticsservice.EventQuery{
		Window: windowToAnalytics(window), EventType: strings.TrimSpace(r.URL.Query().Get("eventType")),
		ActorUserID: strings.TrimSpace(r.URL.Query().Get("actorUserId")), Limit: limit, Offset: offset,
	})
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) analyticsAuditEvents(w http.ResponseWriter, r *http.Request) {
	window, err := analyticsWindow(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, offset, err := analyticsPage(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.analytics.Audit(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), analyticsservice.AuditQuery{
		Window: windowToAnalytics(window), ActorUserID: strings.TrimSpace(r.URL.Query().Get("actorUserId")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")), Outcome: strings.TrimSpace(r.URL.Query().Get("outcome")),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) analyticsRiskEvents(w http.ResponseWriter, r *http.Request) {
	window, err := analyticsWindow(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, offset, err := analyticsPage(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	openOnly := false
	if raw := strings.TrimSpace(r.URL.Query().Get("openOnly")); raw != "" {
		openOnly, err = strconv.ParseBool(raw)
		if err != nil {
			httpx.WriteError(w, r, analyticsQueryProblem("openOnly must be true or false"))
			return
		}
	}
	result, err := g.analytics.Risks(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), analyticsservice.RiskQuery{
		Window: windowToAnalytics(window), OpenOnly: openOnly,
		Severity: strings.TrimSpace(r.URL.Query().Get("severity")), Limit: limit, Offset: offset,
	})
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func analyticsWindow(r *http.Request) (analyticsservice.WindowInput, error) {
	var input analyticsservice.WindowInput
	for name, target := range map[string]*time.Time{"from": &input.From, "to": &input.To} {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return input, analyticsQueryProblem(name + " must be RFC3339")
		}
		*target = value
	}
	return input, nil
}

func analyticsPage(r *http.Request) (int, int, error) {
	values := [2]int{}
	for index, name := range []string{"limit", "offset"} {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, analyticsQueryProblem(name + " must be an integer")
		}
		values[index] = value
	}
	return values[0], values[1], nil
}

func windowToAnalytics(input analyticsservice.WindowInput) analyticsservice.Window {
	return analyticsservice.Window{From: input.From, To: input.To}
}

func analyticsQueryProblem(message string) httpx.Problem {
	return httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: message}
}
