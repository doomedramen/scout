package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
)

func (s *Store) EnsureDefaultAlertRules(ctx context.Context, defaults []AlertRule) ([]AlertRule, error) {
	for _, rule := range defaults {
		if rule.TemplateKey == "" || validateAlertRule(rule) != nil {
			return nil, ErrInvalid
		}
	}
	if s.db != nil {
		return s.ensureDefaultAlertRulesSQL(ctx, defaults)
	}
	var result []AlertRule
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for _, candidate := range defaults {
			found := false
			for _, existing := range state.AlertRules {
				if existing.TemplateKey == candidate.TemplateKey {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if candidate.ID == "" {
				candidate.ID = NewID()
			}
			if candidate.Revision == 0 {
				candidate.Revision = 1
			}
			if candidate.CreatedAt.IsZero() {
				candidate.CreatedAt = now
			}
			if candidate.UpdatedAt.IsZero() {
				candidate.UpdatedAt = candidate.CreatedAt
			}
			state.AlertRules[candidate.ID] = cloneAlertRule(candidate)
		}
		result = sortedAlertRules(state.AlertRules, true)
		return nil
	})
	return result, err
}

func (s *Store) ListAlertRules(ctx context.Context, includeRetired bool) ([]AlertRule, error) {
	if s.db != nil {
		return s.listAlertRulesSQL(ctx, includeRetired)
	}
	var result []AlertRule
	err := s.read(ctx, func(state *State) error {
		result = sortedAlertRules(state.AlertRules, includeRetired)
		return nil
	})
	return result, err
}

func (s *Store) GetAlertRule(ctx context.Context, id string) (AlertRule, error) {
	if strings.TrimSpace(id) == "" {
		return AlertRule{}, ErrInvalid
	}
	if s.db != nil {
		return s.getAlertRuleSQL(ctx, id)
	}
	var result AlertRule
	err := s.read(ctx, func(state *State) error {
		item, ok := state.AlertRules[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneAlertRule(item)
		return nil
	})
	return result, err
}

func (s *Store) PutAlertRule(ctx context.Context, rule AlertRule, expectedRevision int64) (AlertRule, error) {
	if validateAlertRule(rule) != nil {
		return AlertRule{}, ErrInvalid
	}
	if s.db != nil {
		return s.putAlertRuleSQL(ctx, rule, expectedRevision)
	}
	var result AlertRule
	err := s.mutate(ctx, func(state *State) error {
		if err := validateAlertTargetMemory(state, rule.TargetKind, rule.TargetID); err != nil {
			return err
		}
		if rule.TemplateKey != "" {
			for id, existing := range state.AlertRules {
				if id != rule.ID && existing.TemplateKey == rule.TemplateKey {
					return ErrConflict
				}
			}
		}
		current, exists := state.AlertRules[rule.ID]
		if exists {
			if expectedRevision > 0 && current.Revision != expectedRevision {
				return ErrConflict
			}
			rule.CreatedAt = current.CreatedAt
			rule.Revision = current.Revision + 1
			rule.RetiredAt = cloneTime(current.RetiredAt)
			reason := "rule_changed"
			if !rule.Enabled {
				reason = "disabled"
			}
			if _, err := closeIncidentsForState(state, func(incident Incident) bool { return incident.LineageID == current.ID }, reason, s.now().UTC()); err != nil {
				return err
			}
		} else {
			if rule.ID == "" {
				rule.ID = NewID()
			}
			rule.Revision = 1
			if rule.CreatedAt.IsZero() {
				rule.CreatedAt = s.now().UTC()
			}
		}
		rule.UpdatedAt = s.now().UTC()
		state.AlertRules[rule.ID] = cloneAlertRule(rule)
		result = cloneAlertRule(rule)
		return nil
	})
	return result, err
}

func (s *Store) RetireAlertRule(ctx context.Context, id string, expectedRevision int64) (AlertRule, error) {
	if strings.TrimSpace(id) == "" {
		return AlertRule{}, ErrInvalid
	}
	if s.db != nil {
		return s.retireAlertRuleSQL(ctx, id, expectedRevision)
	}
	var result AlertRule
	err := s.mutate(ctx, func(state *State) error {
		rule, ok := state.AlertRules[id]
		if !ok {
			return ErrNotFound
		}
		if expectedRevision > 0 && rule.Revision != expectedRevision {
			return ErrConflict
		}
		when := s.now().UTC()
		rule.Enabled = false
		rule.Revision++
		rule.RetiredAt = &when
		rule.UpdatedAt = when
		if _, err := closeIncidentsForState(state, func(incident Incident) bool { return incident.LineageID == rule.ID }, "retired", when); err != nil {
			return err
		}
		state.AlertRules[id] = cloneAlertRule(rule)
		result = cloneAlertRule(rule)
		return nil
	})
	return result, err
}

func (s *Store) PutAlertOverride(ctx context.Context, override AlertOverride, expectedRevision int64) (AlertOverride, error) {
	if strings.TrimSpace(override.LineageID) == "" {
		return AlertOverride{}, ErrInvalid
	}
	if s.db != nil {
		return s.putAlertOverrideSQL(ctx, override, expectedRevision)
	}
	var result AlertOverride
	err := s.mutate(ctx, func(state *State) error {
		base, ok := state.AlertRules[override.LineageID]
		if !ok {
			return ErrNotFound
		}
		if err := validateAlertOverride(base, override); err != nil {
			return err
		}
		if err := validateAlertOverrideTargetMemory(state, base, override); err != nil {
			return err
		}
		key := alertOverrideKey(override.LineageID, override.TargetKind, override.TargetID)
		current, exists := state.AlertOverrides[key]
		if exists {
			if expectedRevision > 0 && current.Revision != expectedRevision {
				return ErrConflict
			}
			override.ID = current.ID
			override.CreatedAt = current.CreatedAt
			override.Revision = current.Revision + 1
		} else {
			if override.ID == "" {
				override.ID = NewID()
			}
			override.Revision = 1
			if override.CreatedAt.IsZero() {
				override.CreatedAt = s.now().UTC()
			}
		}
		override.UpdatedAt = s.now().UTC()
		state.AlertOverrides[key] = cloneAlertOverride(override)
		result = cloneAlertOverride(override)
		return nil
	})
	return result, err
}

func (s *Store) ListAlertOverrides(ctx context.Context, lineageID string) ([]AlertOverride, error) {
	if s.db != nil {
		return s.listAlertOverridesSQL(ctx, lineageID)
	}
	result := []AlertOverride{}
	err := s.read(ctx, func(state *State) error {
		for _, override := range state.AlertOverrides {
			if lineageID != "" && override.LineageID != lineageID {
				continue
			}
			result = append(result, cloneAlertOverride(override))
		}
		sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
		return nil
	})
	return result, err
}

func (s *Store) DeleteAlertOverride(ctx context.Context, id string, expectedRevision int64) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalid
	}
	if s.db != nil {
		return s.deleteAlertOverrideSQL(ctx, id, expectedRevision)
	}
	return s.mutate(ctx, func(state *State) error {
		for key, override := range state.AlertOverrides {
			if override.ID != id {
				continue
			}
			if expectedRevision > 0 && override.Revision != expectedRevision {
				return ErrConflict
			}
			delete(state.AlertOverrides, key)
			return nil
		}
		return ErrNotFound
	})
}

func validateAlertRule(rule AlertRule) error {
	if strings.TrimSpace(rule.Name) == "" || len(rule.Name) > 120 || len(rule.TemplateKey) > 64 || len(rule.ServicePattern) > 128 || len(rule.CollectorID) > 128 {
		return ErrInvalid
	}
	if rule.Kind != "numeric" && rule.Kind != "state" {
		return ErrInvalid
	}
	if rule.TargetKind != "fleet" && rule.TargetKind != "site" && rule.TargetKind != "device" {
		return ErrInvalid
	}
	if rule.TargetKind == "fleet" && rule.TargetID != "" || rule.TargetKind != "fleet" && strings.TrimSpace(rule.TargetID) == "" {
		return ErrInvalid
	}
	if rule.Severity != "warning" && rule.Severity != "critical" {
		return ErrInvalid
	}
	if rule.TriggerSeconds < 0 || rule.TriggerSeconds > 86400 || rule.ClearSeconds < 0 || rule.ClearSeconds > 86400 || rule.MinimumConsecutiveSamples < 1 || rule.MinimumConsecutiveSamples > 10 {
		return ErrInvalid
	}
	if rule.Kind == "numeric" {
		if rule.ServicePattern != "" || rule.CollectorID != "" {
			return ErrInvalid
		}
		if strings.TrimSpace(rule.Metric) == "" || (rule.Operator != "gt" && rule.Operator != "lt") || rule.TriggerValue == nil || rule.ClearValue == nil || !finiteAlertValue(*rule.TriggerValue) || !finiteAlertValue(*rule.ClearValue) {
			return ErrInvalid
		}
		if rule.Operator == "gt" && *rule.ClearValue >= *rule.TriggerValue || rule.Operator == "lt" && *rule.ClearValue <= *rule.TriggerValue {
			return ErrInvalid
		}
		return nil
	}
	if strings.TrimSpace(rule.TriggerState) == "" || strings.TrimSpace(rule.ClearState) == "" || rule.TriggerState == rule.ClearState {
		return ErrInvalid
	}
	if rule.ServicePattern != "" && !validServicePattern(rule.ServicePattern) {
		return ErrInvalid
	}
	return nil
}

func validateAlertOverride(base AlertRule, override AlertOverride) error {
	if override.LineageID != base.ID || (override.TargetKind != "site" && override.TargetKind != "device") || strings.TrimSpace(override.TargetID) == "" {
		return ErrInvalid
	}
	if alertTargetRank(override.TargetKind) < alertTargetRank(base.TargetKind) || alertTargetRank(override.TargetKind) == alertTargetRank(base.TargetKind) && override.TargetID != base.TargetID {
		return ErrInvalid
	}
	candidate := AlertRule{
		ID: base.ID, Name: base.Name, Kind: override.Kind, Metric: override.Metric, EntityID: override.EntityID, Operator: override.Operator,
		TriggerValue: override.TriggerValue, ClearValue: override.ClearValue, TriggerState: override.TriggerState, ClearState: override.ClearState,
		ServicePattern: override.ServicePattern, CollectorID: override.CollectorID,
		TriggerSeconds: override.TriggerSeconds, ClearSeconds: override.ClearSeconds, MinimumConsecutiveSamples: override.MinimumConsecutiveSamples,
		Severity: override.Severity, TargetKind: base.TargetKind, TargetID: base.TargetID, Enabled: override.Enabled,
	}
	return validateAlertRule(candidate)
}

func validateAlertTargetMemory(state *State, kind, id string) error {
	switch kind {
	case "fleet":
		if id != "" {
			return ErrInvalid
		}
	case "site":
		if _, ok := state.Sites[id]; !ok {
			return ErrNotFound
		}
	case "device":
		if _, ok := state.Devices[id]; !ok {
			return ErrNotFound
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateAlertOverrideTargetMemory(state *State, base AlertRule, override AlertOverride) error {
	if err := validateAlertTargetMemory(state, override.TargetKind, override.TargetID); err != nil {
		return err
	}
	if base.TargetKind == "site" && override.TargetKind == "device" {
		device := state.Devices[override.TargetID]
		if device.SiteID != base.TargetID {
			return ErrConflict
		}
	}
	return nil
}

func alertTargetRank(kind string) int {
	switch kind {
	case "fleet":
		return 0
	case "site":
		return 1
	case "device":
		return 2
	default:
		return -1
	}
}

func alertOverrideKey(lineageID, targetKind, targetID string) string {
	return safeCompositeKey(lineageID, targetKind, targetID)
}

func finiteAlertValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validServicePattern(value string) bool {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_.@?*:-", char) {
			continue
		}
		return false
	}
	return value != ""
}

func sortedAlertRules(values map[string]AlertRule, includeRetired bool) []AlertRule {
	result := make([]AlertRule, 0, len(values))
	for _, rule := range values {
		if !includeRetired && rule.RetiredAt != nil {
			continue
		}
		result = append(result, cloneAlertRule(rule))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].TemplateKey == result[right].TemplateKey {
			return result[left].ID < result[right].ID
		}
		return result[left].TemplateKey < result[right].TemplateKey
	})
	return result
}

func cloneAlertRule(rule AlertRule) AlertRule {
	rule.TriggerValue = cloneAlertFloat(rule.TriggerValue)
	rule.ClearValue = cloneAlertFloat(rule.ClearValue)
	rule.RetiredAt = cloneTime(rule.RetiredAt)
	return rule
}

func cloneAlertOverride(override AlertOverride) AlertOverride {
	override.TriggerValue = cloneAlertFloat(override.TriggerValue)
	override.ClearValue = cloneAlertFloat(override.ClearValue)
	return override
}

func cloneAlertFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

const alertRuleSelectColumns = `id, template_key, name, kind, metric, entity_id, operator, trigger_value, clear_value, trigger_state, clear_state, service_pattern, collector_id, trigger_seconds, clear_seconds, minimum_consecutive_samples, severity, target_kind, target_id, enabled, revision, retired_at, created_at, updated_at`

type alertRuleScanner interface {
	Scan(...any) error
}

func scanAlertRule(scanner alertRuleScanner) (AlertRule, error) {
	var rule AlertRule
	var templateKey sql.NullString
	var retiredAt sql.NullTime
	var triggerNumber, clearNumber sql.NullFloat64
	if err := scanner.Scan(&rule.ID, &templateKey, &rule.Name, &rule.Kind, &rule.Metric, &rule.EntityID, &rule.Operator, &triggerNumber, &clearNumber, &rule.TriggerState, &rule.ClearState, &rule.ServicePattern, &rule.CollectorID, &rule.TriggerSeconds, &rule.ClearSeconds, &rule.MinimumConsecutiveSamples, &rule.Severity, &rule.TargetKind, &rule.TargetID, &rule.Enabled, &rule.Revision, &retiredAt, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return AlertRule{}, err
	}
	if templateKey.Valid {
		rule.TemplateKey = templateKey.String
	}
	if triggerNumber.Valid {
		value := triggerNumber.Float64
		rule.TriggerValue = &value
	}
	if clearNumber.Valid {
		value := clearNumber.Float64
		rule.ClearValue = &value
	}
	if retiredAt.Valid {
		value := retiredAt.Time.UTC()
		rule.RetiredAt = &value
	}
	return rule, nil
}

func alertRuleArgs(rule AlertRule) []any {
	return []any{rule.ID, nullableAlertString(rule.TemplateKey), rule.Name, rule.Kind, rule.Metric, rule.EntityID, rule.Operator, rule.TriggerValue, rule.ClearValue, rule.TriggerState, rule.ClearState, rule.ServicePattern, rule.CollectorID, rule.TriggerSeconds, rule.ClearSeconds, rule.MinimumConsecutiveSamples, rule.Severity, rule.TargetKind, rule.TargetID, rule.Enabled, rule.Revision, rule.RetiredAt, rule.CreatedAt.UTC(), rule.UpdatedAt.UTC()}
}

func nullableAlertString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ensureDefaultAlertRulesSQL(ctx context.Context, defaults []AlertRule) ([]AlertRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, candidate := range defaults {
		if candidate.ID == "" {
			candidate.ID = NewID()
		}
		if candidate.Revision == 0 {
			candidate.Revision = 1
		}
		if candidate.CreatedAt.IsZero() {
			candidate.CreatedAt = s.now().UTC()
		}
		if candidate.UpdatedAt.IsZero() {
			candidate.UpdatedAt = candidate.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_rules (`+alertRuleSelectColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24) ON CONFLICT (template_key) DO NOTHING`, alertRuleArgs(candidate)...); err != nil {
			return nil, mapAlertRuleSQLError(err)
		}
	}
	items, err := listAlertRulesQuery(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit default alert rules: %w", err)
	}
	return items, nil
}

func (s *Store) listAlertRulesSQL(ctx context.Context, includeRetired bool) ([]AlertRule, error) {
	query := `SELECT ` + alertRuleSelectColumns + ` FROM alert_rules`
	if !includeRetired {
		query += ` WHERE retired_at IS NULL`
	}
	query += ` ORDER BY template_key NULLS LAST, id`
	return scanAlertRules(ctx, s.db, query)
}

func listAlertRulesQuery(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, includeRetired bool) ([]AlertRule, error) {
	query := `SELECT ` + alertRuleSelectColumns + ` FROM alert_rules`
	if !includeRetired {
		query += ` WHERE retired_at IS NULL`
	}
	query += ` ORDER BY template_key NULLS LAST, id`
	return scanAlertRules(ctx, queryer, query)
}

func scanAlertRules(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string) ([]AlertRule, error) {
	rows, err := queryer.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer rows.Close()
	result := []AlertRule{}
	for rows.Next() {
		rule, scanErr := scanAlertRule(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan alert rule: %w", scanErr)
		}
		result = append(result, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list alert rules rows: %w", err)
	}
	return result, nil
}

func (s *Store) getAlertRuleSQL(ctx context.Context, id string) (AlertRule, error) {
	rule, err := scanAlertRule(s.db.QueryRowContext(ctx, `SELECT `+alertRuleSelectColumns+` FROM alert_rules WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return AlertRule{}, ErrNotFound
	}
	if err != nil {
		return AlertRule{}, fmt.Errorf("get alert rule: %w", err)
	}
	return rule, nil
}

func (s *Store) putAlertRuleSQL(ctx context.Context, rule AlertRule, expectedRevision int64) (AlertRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertRule{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateAlertTargetSQL(ctx, tx, rule.TargetKind, rule.TargetID); err != nil {
		return AlertRule{}, err
	}
	current, err := scanAlertRule(tx.QueryRowContext(ctx, `SELECT `+alertRuleSelectColumns+` FROM alert_rules WHERE id = $1 FOR UPDATE`, rule.ID))
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return AlertRule{}, fmt.Errorf("read alert rule: %w", err)
	}
	if exists {
		if expectedRevision > 0 && current.Revision != expectedRevision {
			return AlertRule{}, ErrConflict
		}
		rule.CreatedAt = current.CreatedAt
		rule.Revision = current.Revision + 1
		rule.RetiredAt = current.RetiredAt
	} else {
		if rule.ID == "" {
			rule.ID = NewID()
		}
		rule.Revision = 1
		if rule.CreatedAt.IsZero() {
			rule.CreatedAt = s.now().UTC()
		}
	}
	rule.UpdatedAt = s.now().UTC()
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE alert_rules SET template_key=$1, name=$2, kind=$3, metric=$4, entity_id=$5, operator=$6, trigger_value=$7, clear_value=$8, trigger_state=$9, clear_state=$10, service_pattern=$11, collector_id=$12, trigger_seconds=$13, clear_seconds=$14, minimum_consecutive_samples=$15, severity=$16, target_kind=$17, target_id=$18, enabled=$19, revision=$20, retired_at=$21, updated_at=$22 WHERE id=$23`, nullableAlertString(rule.TemplateKey), rule.Name, rule.Kind, rule.Metric, rule.EntityID, rule.Operator, rule.TriggerValue, rule.ClearValue, rule.TriggerState, rule.ClearState, rule.ServicePattern, rule.CollectorID, rule.TriggerSeconds, rule.ClearSeconds, rule.MinimumConsecutiveSamples, rule.Severity, rule.TargetKind, rule.TargetID, rule.Enabled, rule.Revision, rule.RetiredAt, rule.UpdatedAt.UTC(), rule.ID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_rules (`+alertRuleSelectColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`, alertRuleArgs(rule)...)
	}
	if err != nil {
		return AlertRule{}, mapAlertRuleSQLError(err)
	}
	if exists {
		reason := "rule_changed"
		if !rule.Enabled {
			reason = "disabled"
		}
		if _, err := closeIncidentsTx(ctx, tx, "lineage_id=$1", current.ID, reason, rule.UpdatedAt.UTC()); err != nil {
			return AlertRule{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AlertRule{}, fmt.Errorf("commit alert rule: %w", err)
	}
	return rule, nil
}

func (s *Store) retireAlertRuleSQL(ctx context.Context, id string, expectedRevision int64) (AlertRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertRule{}, err
	}
	defer func() { _ = tx.Rollback() }()
	rule, err := scanAlertRule(tx.QueryRowContext(ctx, `SELECT `+alertRuleSelectColumns+` FROM alert_rules WHERE id = $1 FOR UPDATE`, id))
	if err == sql.ErrNoRows {
		return AlertRule{}, ErrNotFound
	}
	if err != nil {
		return AlertRule{}, fmt.Errorf("read alert rule for retirement: %w", err)
	}
	if expectedRevision > 0 && rule.Revision != expectedRevision {
		return AlertRule{}, ErrConflict
	}
	when := s.now().UTC()
	rule.Enabled = false
	rule.Revision++
	rule.RetiredAt = &when
	rule.UpdatedAt = when
	if _, err := tx.ExecContext(ctx, `UPDATE alert_rules SET enabled=false, revision=$1, retired_at=$2, updated_at=$3 WHERE id=$4`, rule.Revision, rule.RetiredAt, rule.UpdatedAt, id); err != nil {
		return AlertRule{}, mapAlertRuleSQLError(err)
	}
	if _, err := closeIncidentsTx(ctx, tx, "lineage_id=$1", id, "retired", when); err != nil {
		return AlertRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return AlertRule{}, fmt.Errorf("commit alert rule retirement: %w", err)
	}
	return rule, nil
}

const alertOverrideSelectColumns = `id, lineage_id, target_kind, target_id, kind, metric, entity_id, operator, trigger_value, clear_value, trigger_state, clear_state, service_pattern, collector_id, trigger_seconds, clear_seconds, minimum_consecutive_samples, severity, enabled, revision, created_at, updated_at`

func scanAlertOverride(scanner alertRuleScanner) (AlertOverride, error) {
	var override AlertOverride
	var triggerNumber, clearNumber sql.NullFloat64
	if err := scanner.Scan(&override.ID, &override.LineageID, &override.TargetKind, &override.TargetID, &override.Kind, &override.Metric, &override.EntityID, &override.Operator, &triggerNumber, &clearNumber, &override.TriggerState, &override.ClearState, &override.ServicePattern, &override.CollectorID, &override.TriggerSeconds, &override.ClearSeconds, &override.MinimumConsecutiveSamples, &override.Severity, &override.Enabled, &override.Revision, &override.CreatedAt, &override.UpdatedAt); err != nil {
		return AlertOverride{}, err
	}
	if triggerNumber.Valid {
		value := triggerNumber.Float64
		override.TriggerValue = &value
	}
	if clearNumber.Valid {
		value := clearNumber.Float64
		override.ClearValue = &value
	}
	return override, nil
}

func alertOverrideArgs(override AlertOverride) []any {
	return []any{override.ID, override.LineageID, override.TargetKind, override.TargetID, override.Kind, override.Metric, override.EntityID, override.Operator, override.TriggerValue, override.ClearValue, override.TriggerState, override.ClearState, override.ServicePattern, override.CollectorID, override.TriggerSeconds, override.ClearSeconds, override.MinimumConsecutiveSamples, override.Severity, override.Enabled, override.Revision, override.CreatedAt.UTC(), override.UpdatedAt.UTC()}
}

func (s *Store) listAlertOverridesSQL(ctx context.Context, lineageID string) ([]AlertOverride, error) {
	query := `SELECT ` + alertOverrideSelectColumns + ` FROM alert_overrides`
	args := []any{}
	if lineageID != "" {
		query += ` WHERE lineage_id = $1`
		args = append(args, lineageID)
	}
	query += ` ORDER BY lineage_id, target_kind, target_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alert overrides: %w", err)
	}
	defer rows.Close()
	result := []AlertOverride{}
	for rows.Next() {
		item, scanErr := scanAlertOverride(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan alert override: %w", scanErr)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list alert overrides rows: %w", err)
	}
	return result, nil
}

func (s *Store) putAlertOverrideSQL(ctx context.Context, override AlertOverride, expectedRevision int64) (AlertOverride, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertOverride{}, err
	}
	defer func() { _ = tx.Rollback() }()
	base, err := scanAlertRule(tx.QueryRowContext(ctx, `SELECT `+alertRuleSelectColumns+` FROM alert_rules WHERE id = $1 FOR UPDATE`, override.LineageID))
	if err == sql.ErrNoRows {
		return AlertOverride{}, ErrNotFound
	}
	if err != nil {
		return AlertOverride{}, fmt.Errorf("read alert override lineage: %w", err)
	}
	if err := validateAlertOverride(base, override); err != nil {
		return AlertOverride{}, err
	}
	if err := validateAlertOverrideTargetSQL(ctx, tx, base, override); err != nil {
		return AlertOverride{}, err
	}
	current, err := scanAlertOverride(tx.QueryRowContext(ctx, `SELECT `+alertOverrideSelectColumns+` FROM alert_overrides WHERE lineage_id = $1 AND target_kind = $2 AND target_id = $3 FOR UPDATE`, override.LineageID, override.TargetKind, override.TargetID))
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return AlertOverride{}, fmt.Errorf("read alert override: %w", err)
	}
	if exists {
		if expectedRevision > 0 && current.Revision != expectedRevision {
			return AlertOverride{}, ErrConflict
		}
		override.ID = current.ID
		override.CreatedAt = current.CreatedAt
		override.Revision = current.Revision + 1
	} else {
		if override.ID == "" {
			override.ID = NewID()
		}
		override.Revision = 1
		if override.CreatedAt.IsZero() {
			override.CreatedAt = s.now().UTC()
		}
	}
	override.UpdatedAt = s.now().UTC()
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE alert_overrides SET kind=$1, metric=$2, entity_id=$3, operator=$4, trigger_value=$5, clear_value=$6, trigger_state=$7, clear_state=$8, service_pattern=$9, collector_id=$10, trigger_seconds=$11, clear_seconds=$12, minimum_consecutive_samples=$13, severity=$14, enabled=$15, revision=$16, updated_at=$17 WHERE id=$18`, override.Kind, override.Metric, override.EntityID, override.Operator, override.TriggerValue, override.ClearValue, override.TriggerState, override.ClearState, override.ServicePattern, override.CollectorID, override.TriggerSeconds, override.ClearSeconds, override.MinimumConsecutiveSamples, override.Severity, override.Enabled, override.Revision, override.UpdatedAt.UTC(), override.ID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_overrides (`+alertOverrideSelectColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`, alertOverrideArgs(override)...)
	}
	if err != nil {
		return AlertOverride{}, mapAlertRuleSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return AlertOverride{}, fmt.Errorf("commit alert override: %w", err)
	}
	return override, nil
}

func (s *Store) deleteAlertOverrideSQL(ctx context.Context, id string, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM alert_overrides WHERE id = $1 FOR UPDATE`, id).Scan(&revision)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read alert override for deletion: %w", err)
	}
	if expectedRevision > 0 && revision != expectedRevision {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_overrides WHERE id = $1`, id); err != nil {
		return mapAlertRuleSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alert override deletion: %w", err)
	}
	return nil
}

func validateAlertTargetSQL(ctx context.Context, tx *sql.Tx, kind, id string) error {
	switch kind {
	case "fleet":
		if id != "" {
			return ErrInvalid
		}
	case "site":
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE id = $1) OR EXISTS (SELECT 1 FROM workspace_state WHERE singleton = true AND state_json->'sites' ? $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("check alert site target: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	case "device":
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1) OR EXISTS (SELECT 1 FROM workspace_state WHERE singleton = true AND state_json->'devices' ? $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("check alert device target: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateAlertOverrideTargetSQL(ctx context.Context, tx *sql.Tx, base AlertRule, override AlertOverride) error {
	if err := validateAlertTargetSQL(ctx, tx, override.TargetKind, override.TargetID); err != nil {
		return err
	}
	if base.TargetKind != "site" || override.TargetKind != "device" {
		return nil
	}
	var siteID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT site_id FROM devices WHERE id = $1), NULLIF((SELECT state_json->'devices'->$1->>'siteId' FROM workspace_state WHERE singleton = true), ''))`, override.TargetID).Scan(&siteID); err != nil {
		return fmt.Errorf("read alert device site: %w", err)
	}
	if !siteID.Valid || siteID.String != base.TargetID {
		return ErrConflict
	}
	return nil
}

func mapAlertRuleSQLError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint") {
		return ErrConflict
	}
	if strings.Contains(message, "foreign key") {
		return ErrNotFound
	}
	return fmt.Errorf("alert rule storage: %w", err)
}
