package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"scout.local/scout/internal/alerts"
	"scout.local/scout/internal/store"
)

const (
	defaultAlertPageSize = 100
	maxAlertPageSize     = 500
)

var allowedAlertMetrics = map[string]struct{}{
	"cpu.utilization": {}, "cpu.user_percent": {}, "cpu.system_percent": {}, "cpu.iowait_percent": {},
	"cpu.steal_percent": {}, "load.1m": {}, "load.5m": {}, "load.15m": {}, "memory.used": {},
	"memory.capacity": {}, "memory.used_percent": {}, "filesystem.used": {}, "filesystem.capacity": {},
	"filesystem.used_percent": {}, "host.uptime": {}, "network.receive_rate": {}, "network.transmit_rate": {},
	"swap.used": {}, "swap.capacity": {}, "disk.read_rate": {}, "disk.write_rate": {}, "disk.utilization": {},
	"disk.read_latency": {}, "disk.write_latency": {}, "smart.temperature": {}, "smart.wear_percent": {},
	"smart.error_count": {}, "zfs.pool.used": {}, "zfs.pool.capacity": {}, "zfs.dataset.used": {},
	"zfs.dataset.available": {}, "zfs.pool.read_rate": {}, "zfs.pool.write_rate": {}, "sensor.temperature": {},
	"sensor.fan_speed": {}, "gpu.utilization": {}, "gpu.memory.used": {}, "gpu.memory.capacity": {},
	"gpu.temperature": {}, "gpu.power": {},
}

var allowedAlertStates = map[string]string{
	"host_offline":              "online",
	"service_failed":            "active",
	"service_required_inactive": "active",
	"collector_degraded":        "healthy",
	"hardware_fault":            "healthy",
}

func (a *App) registerAlertRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/alert-rules", a.listAlertRules)
	mux.HandleFunc("POST /api/v1/alert-rules", a.createAlertRule)
	mux.HandleFunc("GET /api/v1/alert-rules/{ruleId}", a.getAlertRule)
	mux.HandleFunc("PATCH /api/v1/alert-rules/{ruleId}", a.updateAlertRule)
	mux.HandleFunc("DELETE /api/v1/alert-rules/{ruleId}", a.retireAlertRule)
	mux.HandleFunc("GET /api/v1/alert-rules/{ruleId}/overrides", a.listAlertOverrides)
	mux.HandleFunc("POST /api/v1/alert-rules/{ruleId}/overrides", a.createAlertOverride)
	mux.HandleFunc("PATCH /api/v1/alert-rules/{ruleId}/overrides/{overrideId}", a.updateAlertOverride)
	mux.HandleFunc("DELETE /api/v1/alert-rules/{ruleId}/overrides/{overrideId}", a.deleteAlertOverride)
	mux.HandleFunc("GET /api/v1/incidents", a.listIncidents)
	mux.HandleFunc("GET /api/v1/incidents/{incidentId}", a.getIncident)
	mux.HandleFunc("GET /api/v1/incidents/{incidentId}/transitions", a.listIncidentTransitions)
	mux.HandleFunc("POST /api/v1/incidents/{incidentId}/acknowledgment", a.acknowledgeIncident)
}

type alertConditionInput struct {
	Metric                    *string  `json:"metric"`
	EntityID                  *string  `json:"entityId"`
	Operator                  *string  `json:"operator"`
	TriggerValue              *float64 `json:"triggerValue"`
	ClearValue                *float64 `json:"clearValue"`
	State                     *string  `json:"state"`
	ServicePattern            *string  `json:"servicePattern"`
	CollectorID               *string  `json:"collectorId"`
	TriggerSeconds            *int     `json:"triggerSeconds"`
	ClearSeconds              *int     `json:"clearSeconds"`
	MinimumConsecutiveSamples *int     `json:"minimumConsecutiveSamples"`
}

type alertRuleInput struct {
	Name       string               `json:"name"`
	TargetKind string               `json:"targetKind"`
	TargetID   *string              `json:"targetId"`
	Kind       string               `json:"kind"`
	Severity   string               `json:"severity"`
	Enabled    *bool                `json:"enabled"`
	Condition  *alertConditionInput `json:"condition"`
}

type alertRulePatch struct {
	ExpectedRevision int64                `json:"expectedRevision"`
	Name             *string              `json:"name"`
	Severity         *string              `json:"severity"`
	Enabled          *bool                `json:"enabled"`
	Condition        *alertConditionInput `json:"condition"`
}

type alertOverrideInput struct {
	TargetKind string               `json:"targetKind"`
	TargetID   string               `json:"targetId"`
	Severity   *string              `json:"severity"`
	Enabled    *bool                `json:"enabled"`
	Condition  *alertConditionInput `json:"condition"`
}

type alertOverridePatch struct {
	ExpectedRevision int64                `json:"expectedRevision"`
	Severity         *string              `json:"severity"`
	Enabled          *bool                `json:"enabled"`
	Condition        *alertConditionInput `json:"condition"`
}

type expectedRevisionInput struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

func (a *App) listAlertRules(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.Store.ListAlertRules(r.Context(), false)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	var enabled *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("enabled")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
		enabled = &value
	}
	targetKind := strings.TrimSpace(r.URL.Query().Get("targetKind"))
	if targetKind != "" && targetKind != alerts.TargetFleet && targetKind != alerts.TargetSite && targetKind != alerts.TargetDevice {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	targetID := strings.TrimSpace(r.URL.Query().Get("targetId"))
	filtered := make([]store.AlertRule, 0, len(items))
	for _, item := range items {
		if enabled != nil && item.Enabled != *enabled || targetKind != "" && item.TargetKind != targetKind || targetID != "" && item.TargetID != targetID {
			continue
		}
		filtered = append(filtered, item)
	}
	page, pageErr := paginateAlertRules(filtered, cursor, limit)
	if pageErr != nil {
		writeMappedError(w, r, pageErr)
		return
	}
	output := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		output = append(output, alertRuleResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": output, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) createAlertRule(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request alertRuleInput
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	rule, err := request.rule()
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	created, err := a.Store.PutAlertRule(r.Context(), rule, 0)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_rule.create", created.ID, map[string]any{"revision": created.Revision})
	writeJSON(w, http.StatusCreated, alertRuleResponse(created))
}

func (a *App) getAlertRule(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	rule, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, alertRuleResponse(rule))
}

func (a *App) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request alertRulePatch
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.Name == nil && request.Severity == nil && request.Enabled == nil && request.Condition == nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	updated := current
	if request.Name != nil {
		updated.Name = strings.TrimSpace(*request.Name)
	}
	if request.Severity != nil {
		updated.Severity = strings.TrimSpace(*request.Severity)
	}
	if request.Enabled != nil {
		updated.Enabled = *request.Enabled
	}
	if request.Condition != nil {
		condition, conditionErr := request.Condition.ruleFields(current.Kind)
		if conditionErr != nil {
			writeMappedError(w, r, conditionErr)
			return
		}
		applyRuleFields(&updated, condition)
	}
	updated, err = a.Store.PutAlertRule(r.Context(), updated, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_rule.update", updated.ID, map[string]any{"revision": updated.Revision})
	writeJSON(w, http.StatusOK, alertRuleResponse(updated))
}

func (a *App) retireAlertRule(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	rule, err := a.Store.RetireAlertRule(r.Context(), r.PathValue("ruleId"), request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_rule.retire", rule.ID, map[string]any{"revision": rule.Revision})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) listAlertOverrides(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	rule, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.Store.ListAlertOverrides(r.Context(), rule.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	page, err := paginateAlertOverrides(items, cursor, limit)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	output := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		output = append(output, alertOverrideResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": output, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) createAlertOverride(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	rule, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	var request alertOverrideInput
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.Severity == nil || request.Enabled == nil || request.Condition == nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	override, err := request.override(rule)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	existing, listErr := a.Store.ListAlertOverrides(r.Context(), rule.ID)
	if listErr != nil {
		writeMappedError(w, r, listErr)
		return
	}
	for _, item := range existing {
		if item.TargetKind == override.TargetKind && item.TargetID == override.TargetID {
			writeMappedError(w, r, store.ErrConflict)
			return
		}
	}
	created, err := a.Store.PutAlertOverride(r.Context(), override, 0)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_override.create", created.ID, map[string]any{"lineageId": created.LineageID, "revision": created.Revision})
	writeJSON(w, http.StatusCreated, alertOverrideResponse(created))
}

func (a *App) updateAlertOverride(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request alertOverridePatch
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.Severity == nil && request.Enabled == nil && request.Condition == nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	rule, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	current, err := findAlertOverride(r.Context(), a.Store, rule.ID, r.PathValue("overrideId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	updated := current
	if request.Severity != nil {
		updated.Severity = strings.TrimSpace(*request.Severity)
	}
	if request.Enabled != nil {
		updated.Enabled = *request.Enabled
	}
	if request.Condition != nil {
		condition, conditionErr := request.Condition.ruleFields(rule.Kind)
		if conditionErr != nil {
			writeMappedError(w, r, conditionErr)
			return
		}
		applyOverrideFields(&updated, condition)
	}
	updated, err = a.Store.PutAlertOverride(r.Context(), updated, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_override.update", updated.ID, map[string]any{"revision": updated.Revision})
	writeJSON(w, http.StatusOK, alertOverrideResponse(updated))
}

func (a *App) deleteAlertOverride(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	rule, err := a.Store.GetAlertRule(r.Context(), r.PathValue("ruleId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if _, err := findAlertOverride(r.Context(), a.Store, rule.ID, r.PathValue("overrideId")); err != nil {
		writeMappedError(w, r, err)
		return
	}
	if err := a.Store.DeleteAlertOverride(r.Context(), r.PathValue("overrideId"), request.ExpectedRevision); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "alert_override.delete", r.PathValue("overrideId"), map[string]any{"lineageId": rule.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) listIncidents(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	query := store.IncidentQuery{Limit: limit, Cursor: cursor, Status: strings.TrimSpace(r.URL.Query().Get("status")), Severity: strings.TrimSpace(r.URL.Query().Get("severity")), DeviceID: strings.TrimSpace(r.URL.Query().Get("deviceId")), SiteID: strings.TrimSpace(r.URL.Query().Get("siteId"))}
	if query.Status != "" && query.Status != "active" && query.Status != "resolved" && query.Status != "closed" || query.Severity != "" && query.Severity != "warning" && query.Severity != "critical" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("acknowledged")); raw != "" {
		acknowledged, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
		query.Acknowledged = &acknowledged
	}
	page, err := a.Store.ListIncidentPage(r.Context(), query)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.incidentResponses(r, page.Items)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) getIncident(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	item, err := a.Store.GetIncident(r.Context(), r.PathValue("incidentId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.incidentResponses(r, []store.Incident{item})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (a *App) listIncidentTransitions(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	incidentID := r.PathValue("incidentId")
	if _, err := a.Store.GetIncident(r.Context(), incidentID); err != nil {
		writeMappedError(w, r, err)
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	page, err := a.Store.ListIncidentTransitionPage(r.Context(), incidentID, cursor, limit)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, incidentTransitionResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) acknowledgeIncident(w http.ResponseWriter, r *http.Request) {
	session, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	item, err := a.Store.AcknowledgeIncident(r.Context(), r.PathValue("incidentId"), request.ExpectedRevision, session.OwnerID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "incident.acknowledge", item.ID, map[string]any{"revision": item.Revision})
	items, err := a.incidentResponses(r, []store.Incident{item})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (request alertRuleInput) rule() (store.AlertRule, error) {
	if request.Enabled == nil || request.Condition == nil || strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.TargetKind) == "" || strings.TrimSpace(request.Kind) == "" || strings.TrimSpace(request.Severity) == "" {
		return store.AlertRule{}, store.ErrInvalid
	}
	fields, err := request.Condition.ruleFields(request.Kind)
	if err != nil {
		return store.AlertRule{}, err
	}
	targetID := ""
	if request.TargetID != nil {
		targetID = strings.TrimSpace(*request.TargetID)
		if targetID == "" {
			return store.AlertRule{}, store.ErrInvalid
		}
	}
	result := store.AlertRule{ID: "", Name: strings.TrimSpace(request.Name), Kind: strings.TrimSpace(request.Kind), Severity: strings.TrimSpace(request.Severity), TargetKind: strings.TrimSpace(request.TargetKind), TargetID: targetID, Enabled: *request.Enabled}
	applyRuleFields(&result, fields)
	if err := validateAPIAlertRule(result); err != nil {
		return store.AlertRule{}, err
	}
	return result, nil
}

type alertRuleFields struct {
	Metric                    string
	EntityID                  string
	Operator                  string
	TriggerValue              *float64
	ClearValue                *float64
	TriggerState              string
	ClearState                string
	ServicePattern            string
	CollectorID               string
	TriggerSeconds            int
	ClearSeconds              int
	MinimumConsecutiveSamples int
}

func (condition *alertConditionInput) ruleFields(kind string) (alertRuleFields, error) {
	if condition == nil {
		return alertRuleFields{}, store.ErrInvalid
	}
	kind = strings.TrimSpace(kind)
	if condition.EntityID != nil && strings.TrimSpace(*condition.EntityID) == "" {
		return alertRuleFields{}, store.ErrInvalid
	}
	result := alertRuleFields{MinimumConsecutiveSamples: 1}
	switch kind {
	case string(alerts.RuleKindNumeric):
		if condition.State != nil || condition.ServicePattern != nil || condition.CollectorID != nil || condition.MinimumConsecutiveSamples != nil || condition.Metric == nil || condition.Operator == nil || condition.TriggerValue == nil || condition.ClearValue == nil || condition.TriggerSeconds == nil || condition.ClearSeconds == nil {
			return alertRuleFields{}, store.ErrInvalid
		}
		result.Metric = strings.TrimSpace(*condition.Metric)
		result.EntityID = optionalTrimmed(condition.EntityID)
		result.Operator = strings.TrimSpace(*condition.Operator)
		result.TriggerValue = cloneFloat(condition.TriggerValue)
		result.ClearValue = cloneFloat(condition.ClearValue)
		result.TriggerSeconds = *condition.TriggerSeconds
		result.ClearSeconds = *condition.ClearSeconds
	case string(alerts.RuleKindState):
		if condition.Metric != nil || condition.Operator != nil || condition.TriggerValue != nil || condition.ClearValue != nil || condition.State == nil || condition.TriggerSeconds == nil || condition.ClearSeconds == nil {
			return alertRuleFields{}, store.ErrInvalid
		}
		state := strings.TrimSpace(*condition.State)
		clearState, known := allowedAlertStates[state]
		if !known {
			return alertRuleFields{}, store.ErrInvalid
		}
		result.TriggerState = state
		result.ClearState = clearState
		result.EntityID = optionalTrimmed(condition.EntityID)
		result.ServicePattern = optionalTrimmed(condition.ServicePattern)
		result.CollectorID = optionalTrimmed(condition.CollectorID)
		if condition.ServicePattern != nil && result.ServicePattern == "" || condition.CollectorID != nil && result.CollectorID == "" {
			return alertRuleFields{}, store.ErrInvalid
		}
		result.TriggerSeconds = *condition.TriggerSeconds
		result.ClearSeconds = *condition.ClearSeconds
		if condition.MinimumConsecutiveSamples != nil {
			result.MinimumConsecutiveSamples = *condition.MinimumConsecutiveSamples
		}
	default:
		return alertRuleFields{}, store.ErrInvalid
	}
	return result, nil
}

func (request alertOverrideInput) override(base store.AlertRule) (store.AlertOverride, error) {
	if request.Severity == nil || request.Enabled == nil || request.Condition == nil {
		return store.AlertOverride{}, store.ErrInvalid
	}
	fields, err := request.Condition.ruleFields(base.Kind)
	if err != nil {
		return store.AlertOverride{}, err
	}
	result := store.AlertOverride{LineageID: base.ID, TargetKind: strings.TrimSpace(request.TargetKind), TargetID: strings.TrimSpace(request.TargetID), Kind: base.Kind, Severity: strings.TrimSpace(*request.Severity), Enabled: *request.Enabled}
	applyOverrideFields(&result, fields)
	if err := validateAPIAlertOverride(base, result); err != nil {
		return store.AlertOverride{}, err
	}
	return result, nil
}

func applyRuleFields(rule *store.AlertRule, fields alertRuleFields) {
	rule.Metric = fields.Metric
	rule.EntityID = fields.EntityID
	rule.Operator = fields.Operator
	rule.TriggerValue = cloneFloat(fields.TriggerValue)
	rule.ClearValue = cloneFloat(fields.ClearValue)
	rule.TriggerState = fields.TriggerState
	rule.ClearState = fields.ClearState
	rule.ServicePattern = fields.ServicePattern
	rule.CollectorID = fields.CollectorID
	rule.TriggerSeconds = fields.TriggerSeconds
	rule.ClearSeconds = fields.ClearSeconds
	rule.MinimumConsecutiveSamples = fields.MinimumConsecutiveSamples
}

func applyOverrideFields(override *store.AlertOverride, fields alertRuleFields) {
	override.Metric = fields.Metric
	override.EntityID = fields.EntityID
	override.Operator = fields.Operator
	override.TriggerValue = cloneFloat(fields.TriggerValue)
	override.ClearValue = cloneFloat(fields.ClearValue)
	override.TriggerState = fields.TriggerState
	override.ClearState = fields.ClearState
	override.ServicePattern = fields.ServicePattern
	override.CollectorID = fields.CollectorID
	override.TriggerSeconds = fields.TriggerSeconds
	override.ClearSeconds = fields.ClearSeconds
	override.MinimumConsecutiveSamples = fields.MinimumConsecutiveSamples
}

func validateAPIAlertRule(rule store.AlertRule) error {
	if rule.Kind == string(alerts.RuleKindNumeric) {
		if _, ok := allowedAlertMetrics[rule.Metric]; !ok {
			return store.ErrInvalid
		}
	}
	if err := alerts.ValidateRule(rule); err != nil {
		return store.ErrInvalid
	}
	return nil
}

func validateAPIAlertOverride(base store.AlertRule, override store.AlertOverride) error {
	if _, ok := allowedAlertStates[override.TriggerState]; override.Kind == string(alerts.RuleKindState) && !ok {
		return store.ErrInvalid
	}
	if override.Kind == string(alerts.RuleKindNumeric) {
		if _, ok := allowedAlertMetrics[override.Metric]; !ok {
			return store.ErrInvalid
		}
	}
	if err := alerts.ValidateOverride(base, override); err != nil {
		return store.ErrInvalid
	}
	return nil
}

func findAlertOverride(ctx context.Context, repository *store.Store, lineageID, id string) (store.AlertOverride, error) {
	items, err := repository.ListAlertOverrides(ctx, lineageID)
	if err != nil {
		return store.AlertOverride{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return store.AlertOverride{}, store.ErrNotFound
}

func alertRuleResponse(rule store.AlertRule) map[string]any {
	response := map[string]any{
		"id": rule.ID, "name": rule.Name, "targetKind": rule.TargetKind, "kind": rule.Kind, "severity": rule.Severity,
		"enabled": rule.Enabled, "condition": alertConditionResponse(rule), "revision": rule.Revision, "createdAt": rule.CreatedAt.UTC(), "updatedAt": rule.UpdatedAt.UTC(),
	}
	if rule.TargetID != "" {
		response["targetId"] = rule.TargetID
	}
	if rule.RetiredAt != nil {
		response["retiredAt"] = rule.RetiredAt.UTC()
	}
	return response
}

func alertOverrideResponse(override store.AlertOverride) map[string]any {
	return map[string]any{
		"id": override.ID, "lineageId": override.LineageID, "targetKind": override.TargetKind, "targetId": override.TargetID,
		"severity": override.Severity, "enabled": override.Enabled, "condition": alertConditionResponseFromOverride(override), "revision": override.Revision,
		"createdAt": override.CreatedAt.UTC(), "updatedAt": override.UpdatedAt.UTC(),
	}
}

func alertConditionResponse(rule store.AlertRule) map[string]any {
	if rule.Kind == string(alerts.RuleKindNumeric) {
		result := map[string]any{"metric": rule.Metric, "operator": rule.Operator, "triggerValue": rule.TriggerValue, "clearValue": rule.ClearValue, "triggerSeconds": rule.TriggerSeconds, "clearSeconds": rule.ClearSeconds}
		if rule.EntityID != "" {
			result["entityId"] = rule.EntityID
		}
		return result
	}
	result := map[string]any{"state": apiStateName(rule.TriggerState), "triggerSeconds": rule.TriggerSeconds, "clearSeconds": rule.ClearSeconds}
	if rule.EntityID != "" {
		result["entityId"] = rule.EntityID
	}
	if rule.ServicePattern != "" {
		result["servicePattern"] = rule.ServicePattern
	}
	if rule.CollectorID != "" {
		result["collectorId"] = rule.CollectorID
	}
	if rule.MinimumConsecutiveSamples > 1 {
		result["minimumConsecutiveSamples"] = rule.MinimumConsecutiveSamples
	}
	return result
}

func alertConditionResponseFromOverride(override store.AlertOverride) map[string]any {
	rule := store.AlertRule{Kind: override.Kind, Metric: override.Metric, EntityID: override.EntityID, Operator: override.Operator, TriggerValue: override.TriggerValue, ClearValue: override.ClearValue, TriggerState: override.TriggerState, ClearState: override.ClearState, ServicePattern: override.ServicePattern, CollectorID: override.CollectorID, TriggerSeconds: override.TriggerSeconds, ClearSeconds: override.ClearSeconds, MinimumConsecutiveSamples: override.MinimumConsecutiveSamples}
	return alertConditionResponse(rule)
}

func apiStateName(value string) string {
	switch value {
	case "offline":
		return "host_offline"
	case "failed":
		return "service_failed"
	case "degraded":
		return "collector_degraded"
	case "fault":
		return "hardware_fault"
	default:
		return value
	}
}

func (a *App) incidentResponses(r *http.Request, incidents []store.Incident) ([]any, error) {
	result := make([]any, 0, len(incidents))
	for _, item := range incidents {
		response := incidentResponse(item)
		decision, suppressionErr := alerts.EvaluateIncidentSuppression(r.Context(), a.Store, item, a.Store.Now())
		if suppressionErr != nil {
			return nil, suppressionErr
		}
		response["notificationSuppression"] = map[string]any{"suppressed": decision.Suppressed, "reasons": decision.Reasons}
		if item.DeviceID != "" {
			device, err := a.Store.GetDevice(r.Context(), item.DeviceID)
			if err == nil && device.SiteID != "" {
				response["siteId"] = device.SiteID
			}
			if err != nil && err != store.ErrNotFound {
				return nil, err
			}
		}
		result = append(result, response)
	}
	return result, nil
}

func incidentResponse(item store.Incident) map[string]any {
	response := map[string]any{
		"id": item.ID, "lineageId": item.LineageID, "entityId": item.EntityID, "deviceId": item.DeviceID, "siteId": nil,
		"status": item.Status, "severity": item.Severity, "openedAt": item.OpenedAt.UTC(), "observedAt": item.ObservedAt.UTC(),
		"evidenceState": item.EvidenceState, "notificationSuppression": map[string]any{"suppressed": false, "reasons": []string{}}, "revision": item.Revision,
		"ruleSnapshot": apiRuleSnapshot(item.RuleSnapshot),
	}
	if item.AcknowledgedAt != nil {
		response["acknowledgedAt"] = item.AcknowledgedAt.UTC()
	}
	if item.AcknowledgedBy != "" {
		response["acknowledgedBy"] = item.AcknowledgedBy
	}
	if item.ClosedAt != nil {
		response["closedAt"] = item.ClosedAt.UTC()
	}
	if item.CloseReason != "" {
		response["closeReason"] = item.CloseReason
	}
	if evidence := incidentEvidence(item); evidence != nil {
		response["evidence"] = evidence
	}
	return response
}

func apiRuleSnapshot(snapshot map[string]any) map[string]any {
	if snapshot == nil {
		return map[string]any{"name": "Alert rule", "kind": string(alerts.RuleKindState), "condition": map[string]any{"state": "hardware_fault", "triggerSeconds": 0, "clearSeconds": 0}}
	}
	name, _ := snapshot["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = "Alert rule"
	}
	kind, _ := snapshot["kind"].(string)
	if kind != string(alerts.RuleKindNumeric) && kind != string(alerts.RuleKindState) {
		kind = string(alerts.RuleKindState)
	}
	condition := map[string]any{}
	if kind == string(alerts.RuleKindNumeric) {
		if value, ok := snapshot["metric"].(string); ok {
			condition["metric"] = value
		}
		if value, ok := snapshot["operator"].(string); ok {
			condition["operator"] = value
		}
		if value, ok := snapshot["triggerValue"]; ok {
			condition["triggerValue"] = value
		}
		if value, ok := snapshot["clearValue"]; ok {
			condition["clearValue"] = value
		}
		if value, ok := snapshot["entityId"].(string); ok && value != "" {
			condition["entityId"] = value
		}
		condition["triggerSeconds"] = integerSnapshotValue(snapshot["triggerSeconds"])
		condition["clearSeconds"] = integerSnapshotValue(snapshot["clearSeconds"])
	} else {
		state, _ := snapshot["triggerState"].(string)
		condition["state"] = apiStateName(state)
		condition["triggerSeconds"] = integerSnapshotValue(snapshot["triggerSeconds"])
		condition["clearSeconds"] = integerSnapshotValue(snapshot["clearSeconds"])
		if value, ok := snapshot["entityId"].(string); ok && value != "" {
			condition["entityId"] = value
		}
		if value, ok := snapshot["servicePattern"].(string); ok && value != "" {
			condition["servicePattern"] = value
		}
		if value, ok := snapshot["collectorId"].(string); ok && value != "" {
			condition["collectorId"] = value
		}
		if value := integerSnapshotValue(snapshot["minimumConsecutiveSamples"]); value > 1 {
			condition["minimumConsecutiveSamples"] = value
		}
	}
	return map[string]any{"name": name, "kind": kind, "condition": condition}
}

func incidentEvidence(item store.Incident) map[string]any {
	if item.Value == nil && item.Unit == "" && item.Source == "" && item.ObservedAt.IsZero() {
		return nil
	}
	result := map[string]any{}
	if item.Value != nil {
		result["value"] = *item.Value
	}
	if item.Unit != "" {
		result["unit"] = item.Unit
	}
	if item.Source != "" {
		result["source"] = item.Source
	}
	if !item.ObservedAt.IsZero() {
		result["observedAt"] = item.ObservedAt.UTC()
	}
	if value, ok := item.RuleSnapshot["triggerState"].(string); ok && value != "" {
		result["rawState"] = value
	}
	if value, ok := item.RuleSnapshot["triggerValue"]; ok {
		result["threshold"] = value
	}
	return result
}

func incidentTransitionResponse(item store.IncidentTransition) map[string]any {
	kind := item.Kind
	if kind == "evidence_state" {
		kind = item.EvidenceState
	}
	if kind == "rule_changed" || kind == "rule_disabled" {
		kind = "administrative_close"
	}
	response := map[string]any{"id": item.ID, "incidentId": item.IncidentID, "sequence": item.Sequence, "kind": kind, "occurredAt": item.OccurredAt.UTC(), "effectiveRevision": maxInt64(item.RuleRevision, 1)}
	if item.Actor != "" {
		response["actor"] = item.Actor
	}
	if item.Value != nil || item.ObservedAt != nil {
		evidence := map[string]any{}
		if item.Value != nil {
			evidence["value"] = *item.Value
		}
		if item.ObservedAt != nil {
			evidence["observedAt"] = item.ObservedAt.UTC()
		}
		response["evidence"] = evidence
	}
	return response
}

func alertPageParams(r *http.Request) (int, string, error) {
	limit := defaultAlertPageSize
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxAlertPageSize {
			return 0, "", store.ErrInvalid
		}
		limit = value
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if len(cursor) > 512 {
		return 0, "", store.ErrInvalid
	}
	return limit, cursor, nil
}

type alertPage[T any] struct {
	Items      []T
	NextCursor string
}

func paginateAlertRules(items []store.AlertRule, cursor string, limit int) (alertPage[store.AlertRule], error) {
	start, err := alertCursorStart(cursor, 2)
	if err != nil {
		return alertPage[store.AlertRule]{}, err
	}
	if start != nil {
		filtered := items[:0]
		for _, item := range items {
			if alertRuleKey(item) > strings.Join(start, "\x00") {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	page := alertPage[store.AlertRule]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeAlertCursor([]string{page.Items[len(page.Items)-1].TemplateKey, page.Items[len(page.Items)-1].ID})
	}
	return page, nil
}

func paginateAlertOverrides(items []store.AlertOverride, cursor string, limit int) (alertPage[store.AlertOverride], error) {
	start, err := alertCursorStart(cursor, 3)
	if err != nil {
		return alertPage[store.AlertOverride]{}, err
	}
	if start != nil {
		filtered := items[:0]
		for _, item := range items {
			if alertOverrideKey(item) > strings.Join(start, "\x00") {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	page := alertPage[store.AlertOverride]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeAlertCursor([]string{last.TargetKind, last.TargetID, last.ID})
	}
	return page, nil
}

func alertRuleKey(item store.AlertRule) string {
	return item.TemplateKey + "\x00" + item.ID
}

func alertOverrideKey(item store.AlertOverride) string {
	return item.TargetKind + "\x00" + item.TargetID + "\x00" + item.ID
}

func encodeAlertCursor(parts []string) string {
	data, _ := json.Marshal(parts)
	return base64.RawURLEncoding.EncodeToString(data)
}

func alertCursorStart(value string, expected int) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, store.ErrInvalid
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != expected {
		return nil, store.ErrInvalid
	}
	return parts, nil
}

func nullableCursor(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func optionalTrimmed(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func integerSnapshotValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
