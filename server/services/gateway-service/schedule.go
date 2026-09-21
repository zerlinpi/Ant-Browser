package gatewayservice

import (
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

func (g *Gateway) listSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := g.schedules.List(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}
func (g *Gateway) createSchedule(w http.ResponseWriter, r *http.Request) {
	var input scheduleservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.schedules.Create(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}
func (g *Gateway) getSchedule(w http.ResponseWriter, r *http.Request) {
	item, err := g.schedules.Get(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("scheduleID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}
func (g *Gateway) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var input scheduleservice.UpdateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.schedules.Update(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("scheduleID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}
func (g *Gateway) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := g.schedules.Delete(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("scheduleID")); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (g *Gateway) enableSchedule(w http.ResponseWriter, r *http.Request) {
	g.toggleSchedule(w, r, true)
}
func (g *Gateway) disableSchedule(w http.ResponseWriter, r *http.Request) {
	g.toggleSchedule(w, r, false)
}
func (g *Gateway) toggleSchedule(w http.ResponseWriter, r *http.Request, enabled bool) {
	var item scheduleservice.Schedule
	var err error
	if enabled {
		item, err = g.schedules.Enable(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("scheduleID"))
	} else {
		item, err = g.schedules.Disable(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("scheduleID"))
	}
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}
