package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MaxSuppressionWindows      = 1000
	defaultSuppressionPageSize = 100
	maxSuppressionPageSize     = 500
)

func (s *Store) ListSuppressionWindows(ctx context.Context, query SuppressionWindowQuery) (SuppressionWindowPage, error) {
	query = normalizeSuppressionWindowQuery(query)
	if query.Limit > maxSuppressionPageSize || len(query.Cursor) > 512 {
		return SuppressionWindowPage{}, ErrInvalid
	}
	if s.db != nil {
		return s.listSuppressionWindowsSQL(ctx, query)
	}
	cursor, err := decodeSuppressionWindowCursor(query.Cursor)
	if err != nil {
		return SuppressionWindowPage{}, err
	}
	items := []SuppressionWindow{}
	err = s.read(ctx, func(state *State) error {
		for _, item := range state.SuppressionWindows {
			if !query.IncludeRetired && item.RetiredAt != nil {
				continue
			}
			if cursor != nil && !suppressionWindowAfterCursor(item, *cursor) {
				continue
			}
			items = append(items, cloneSuppressionWindow(item))
		}
		sortSuppressionWindows(items)
		return nil
	})
	if err != nil {
		return SuppressionWindowPage{}, err
	}
	return suppressionWindowPage(items, query.Limit), nil
}

func (s *Store) GetSuppressionWindow(ctx context.Context, id string) (SuppressionWindow, error) {
	if strings.TrimSpace(id) == "" {
		return SuppressionWindow{}, ErrInvalid
	}
	if s.db != nil {
		return s.getSuppressionWindowSQL(ctx, id)
	}
	var result SuppressionWindow
	err := s.read(ctx, func(state *State) error {
		item, ok := state.SuppressionWindows[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneSuppressionWindow(item)
		return nil
	})
	return result, err
}

func (s *Store) PutSuppressionWindow(ctx context.Context, window SuppressionWindow, expectedRevision int64) (SuppressionWindow, error) {
	window = normalizeSuppressionWindow(window)
	if err := validateSuppressionWindowShape(window); err != nil {
		return SuppressionWindow{}, err
	}
	if s.db != nil {
		return s.putSuppressionWindowSQL(ctx, window, expectedRevision)
	}
	var result SuppressionWindow
	err := s.mutate(ctx, func(state *State) error {
		if err := validateSuppressionTargetMemory(state, window.TargetKind, window.TargetID); err != nil {
			return err
		}
		if window.ID == "" {
			window.ID = NewID()
		}
		current, exists := state.SuppressionWindows[window.ID]
		if exists {
			if current.RetiredAt != nil {
				return ErrConflict
			}
			if expectedRevision > 0 && current.Revision != expectedRevision {
				return ErrConflict
			}
			window.CreatedAt = current.CreatedAt
			window.Revision = current.Revision + 1
		} else {
			active := 0
			for _, item := range state.SuppressionWindows {
				if item.RetiredAt == nil {
					active++
				}
			}
			if active >= MaxSuppressionWindows {
				return ErrBackpressure
			}
			window.Revision = 1
			if window.CreatedAt.IsZero() {
				window.CreatedAt = s.now().UTC()
			}
		}
		window.UpdatedAt = s.now().UTC()
		state.SuppressionWindows[window.ID] = cloneSuppressionWindow(window)
		result = cloneSuppressionWindow(window)
		return nil
	})
	return result, err
}

func (s *Store) RetireSuppressionWindow(ctx context.Context, id string, expectedRevision int64) (SuppressionWindow, error) {
	if strings.TrimSpace(id) == "" {
		return SuppressionWindow{}, ErrInvalid
	}
	if s.db != nil {
		return s.retireSuppressionWindowSQL(ctx, id, expectedRevision)
	}
	var result SuppressionWindow
	err := s.mutate(ctx, func(state *State) error {
		window, ok := state.SuppressionWindows[id]
		if !ok {
			return ErrNotFound
		}
		if expectedRevision > 0 && window.Revision != expectedRevision {
			return ErrConflict
		}
		if window.RetiredAt == nil {
			when := s.now().UTC()
			window.Enabled = false
			window.Revision++
			window.RetiredAt = &when
			window.UpdatedAt = when
			state.SuppressionWindows[id] = cloneSuppressionWindow(window)
		}
		result = cloneSuppressionWindow(window)
		return nil
	})
	return result, err
}

func normalizeSuppressionWindowQuery(query SuppressionWindowQuery) SuppressionWindowQuery {
	if query.Limit <= 0 {
		query.Limit = defaultSuppressionPageSize
	}
	return query
}

func normalizeSuppressionWindow(window SuppressionWindow) SuppressionWindow {
	window.ID = strings.TrimSpace(window.ID)
	window.Name = strings.TrimSpace(window.Name)
	window.TargetKind = strings.TrimSpace(window.TargetKind)
	window.TargetID = strings.TrimSpace(window.TargetID)
	window.Mode = strings.TrimSpace(window.Mode)
	window.Timezone = strings.TrimSpace(window.Timezone)
	window.StartLocal = strings.TrimSpace(window.StartLocal)
	window.EndLocal = strings.TrimSpace(window.EndLocal)
	window.Weekdays = append([]int(nil), window.Weekdays...)
	if window.Weekdays == nil {
		window.Weekdays = []int{}
	}
	sort.Ints(window.Weekdays)
	if window.StartsAt != nil {
		value := window.StartsAt.UTC()
		window.StartsAt = &value
	}
	if window.EndsAt != nil {
		value := window.EndsAt.UTC()
		window.EndsAt = &value
	}
	return window
}

func validateSuppressionWindowShape(window SuppressionWindow) error {
	if window.ID != "" && len(window.ID) > 128 || window.Name == "" || len(window.Name) > 120 || window.Revision < 0 {
		return ErrInvalid
	}
	if window.TargetKind != "fleet" && window.TargetKind != "site" && window.TargetKind != "device" {
		return ErrInvalid
	}
	if window.TargetKind == "fleet" && window.TargetID != "" || window.TargetKind != "fleet" && window.TargetID == "" {
		return ErrInvalid
	}
	switch window.Mode {
	case SuppressionWindowRecurring:
		if window.Timezone == "" || len(window.Timezone) > 128 || len(window.Weekdays) == 0 || len(window.Weekdays) > 7 || window.StartsAt != nil || window.EndsAt != nil || !validLocalClock(window.StartLocal) || !validLocalClock(window.EndLocal) || window.StartLocal == window.EndLocal {
			return ErrInvalid
		}
		if _, err := time.LoadLocation(window.Timezone); err != nil {
			return ErrInvalid
		}
		seen := map[int]struct{}{}
		for _, day := range window.Weekdays {
			if day < 1 || day > 7 {
				return ErrInvalid
			}
			if _, exists := seen[day]; exists {
				return ErrInvalid
			}
			seen[day] = struct{}{}
		}
	case SuppressionWindowOneTime:
		if window.Timezone != "" || len(window.Weekdays) != 0 || window.StartLocal != "" || window.EndLocal != "" || window.StartsAt == nil || window.EndsAt == nil || !window.StartsAt.Before(*window.EndsAt) || window.EndsAt.Sub(*window.StartsAt) > 365*24*time.Hour {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validLocalClock(value string) bool {
	if len(value) != 5 || value[2] != ':' || value[0] < '0' || value[0] > '2' || value[1] < '0' || value[1] > '9' || value[3] < '0' || value[3] > '5' || value[4] < '0' || value[4] > '9' {
		return false
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	return hour <= 23
}

func validateSuppressionTargetMemory(state *State, kind, id string) error {
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

const suppressionWindowSelectColumns = `id, name, target_kind, target_id, mode, timezone, weekdays, start_local, end_local, starts_at, ends_at, enabled, revision, retired_at, created_at, updated_at`

type suppressionWindowScanner interface {
	Scan(...any) error
}

func scanSuppressionWindow(scanner suppressionWindowScanner) (SuppressionWindow, error) {
	var window SuppressionWindow
	var weekdays []byte
	var startsAt, endsAt, retiredAt sql.NullTime
	if err := scanner.Scan(&window.ID, &window.Name, &window.TargetKind, &window.TargetID, &window.Mode, &window.Timezone, &weekdays, &window.StartLocal, &window.EndLocal, &startsAt, &endsAt, &window.Enabled, &window.Revision, &retiredAt, &window.CreatedAt, &window.UpdatedAt); err != nil {
		return SuppressionWindow{}, err
	}
	if len(weekdays) > 0 {
		if err := json.Unmarshal(weekdays, &window.Weekdays); err != nil {
			return SuppressionWindow{}, fmt.Errorf("decode suppression weekdays: %w", err)
		}
	}
	if startsAt.Valid {
		value := startsAt.Time.UTC()
		window.StartsAt = &value
	}
	if endsAt.Valid {
		value := endsAt.Time.UTC()
		window.EndsAt = &value
	}
	if retiredAt.Valid {
		value := retiredAt.Time.UTC()
		window.RetiredAt = &value
	}
	return normalizeSuppressionWindow(window), nil
}

func (s *Store) listSuppressionWindowsSQL(ctx context.Context, query SuppressionWindowQuery) (SuppressionWindowPage, error) {
	args := []any{}
	where := []string{}
	if !query.IncludeRetired {
		where = append(where, "retired_at IS NULL")
	}
	if query.Cursor != "" {
		cursor, err := decodeSuppressionWindowCursor(query.Cursor)
		if err != nil {
			return SuppressionWindowPage{}, err
		}
		args = append(args, cursor.Name, cursor.ID)
		where = append(where, fmt.Sprintf("(lower(name), id) > (lower($%d), $%d)", len(args)-1, len(args)))
	}
	statement := `SELECT ` + suppressionWindowSelectColumns + ` FROM suppression_windows`
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += fmt.Sprintf(" ORDER BY lower(name), id LIMIT %d", query.Limit+1)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return SuppressionWindowPage{}, fmt.Errorf("list suppression windows: %w", err)
	}
	defer rows.Close()
	items := []SuppressionWindow{}
	for rows.Next() {
		item, scanErr := scanSuppressionWindow(rows)
		if scanErr != nil {
			return SuppressionWindowPage{}, fmt.Errorf("scan suppression window: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return SuppressionWindowPage{}, fmt.Errorf("iterate suppression windows: %w", err)
	}
	return suppressionWindowPage(items, query.Limit), nil
}

func (s *Store) getSuppressionWindowSQL(ctx context.Context, id string) (SuppressionWindow, error) {
	item, err := scanSuppressionWindow(s.db.QueryRowContext(ctx, `SELECT `+suppressionWindowSelectColumns+` FROM suppression_windows WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SuppressionWindow{}, ErrNotFound
	}
	if err != nil {
		return SuppressionWindow{}, fmt.Errorf("get suppression window: %w", err)
	}
	return item, nil
}

func (s *Store) putSuppressionWindowSQL(ctx context.Context, window SuppressionWindow, expectedRevision int64) (SuppressionWindow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SuppressionWindow{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if window.ID == "" {
		window.ID = NewID()
	}
	current, err := scanSuppressionWindow(tx.QueryRowContext(ctx, `SELECT `+suppressionWindowSelectColumns+` FROM suppression_windows WHERE id=$1 FOR UPDATE`, window.ID))
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SuppressionWindow{}, fmt.Errorf("read suppression window: %w", err)
	}
	if exists {
		if current.RetiredAt != nil {
			return SuppressionWindow{}, ErrConflict
		}
		if expectedRevision > 0 && current.Revision != expectedRevision {
			return SuppressionWindow{}, ErrConflict
		}
		window.CreatedAt = current.CreatedAt
		window.Revision = current.Revision + 1
	} else {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suppression_windows WHERE retired_at IS NULL`).Scan(&active); err != nil {
			return SuppressionWindow{}, fmt.Errorf("count suppression windows: %w", err)
		}
		if active >= MaxSuppressionWindows {
			return SuppressionWindow{}, ErrBackpressure
		}
		window.Revision = 1
		if window.CreatedAt.IsZero() {
			window.CreatedAt = s.now().UTC()
		}
	}
	if err := validateSuppressionTargetSQL(ctx, tx, window.TargetKind, window.TargetID); err != nil {
		return SuppressionWindow{}, err
	}
	window.UpdatedAt = s.now().UTC()
	weekdays, err := json.Marshal(window.Weekdays)
	if err != nil {
		return SuppressionWindow{}, fmt.Errorf("encode suppression weekdays: %w", err)
	}
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE suppression_windows SET name=$1, target_kind=$2, target_id=$3, mode=$4, timezone=$5, weekdays=$6, start_local=$7, end_local=$8, starts_at=$9, ends_at=$10, enabled=$11, revision=$12, updated_at=$13 WHERE id=$14`, window.Name, window.TargetKind, window.TargetID, window.Mode, window.Timezone, weekdays, window.StartLocal, window.EndLocal, window.StartsAt, window.EndsAt, window.Enabled, window.Revision, window.UpdatedAt, window.ID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO suppression_windows (id, name, target_kind, target_id, mode, timezone, weekdays, start_local, end_local, starts_at, ends_at, enabled, revision, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, window.ID, window.Name, window.TargetKind, window.TargetID, window.Mode, window.Timezone, weekdays, window.StartLocal, window.EndLocal, window.StartsAt, window.EndsAt, window.Enabled, window.Revision, window.CreatedAt, window.UpdatedAt)
	}
	if err != nil {
		return SuppressionWindow{}, mapSuppressionSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return SuppressionWindow{}, fmt.Errorf("commit suppression window: %w", err)
	}
	return window, nil
}

func (s *Store) retireSuppressionWindowSQL(ctx context.Context, id string, expectedRevision int64) (SuppressionWindow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SuppressionWindow{}, err
	}
	defer func() { _ = tx.Rollback() }()
	window, err := scanSuppressionWindow(tx.QueryRowContext(ctx, `SELECT `+suppressionWindowSelectColumns+` FROM suppression_windows WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SuppressionWindow{}, ErrNotFound
	}
	if err != nil {
		return SuppressionWindow{}, fmt.Errorf("read suppression window for retirement: %w", err)
	}
	if expectedRevision > 0 && window.Revision != expectedRevision {
		return SuppressionWindow{}, ErrConflict
	}
	if window.RetiredAt == nil {
		when := s.now().UTC()
		window.Enabled = false
		window.Revision++
		window.RetiredAt = &when
		window.UpdatedAt = when
		if _, err := tx.ExecContext(ctx, `UPDATE suppression_windows SET enabled=false, revision=$1, retired_at=$2, updated_at=$3 WHERE id=$4`, window.Revision, window.RetiredAt, window.UpdatedAt, id); err != nil {
			return SuppressionWindow{}, mapSuppressionSQLError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SuppressionWindow{}, fmt.Errorf("commit suppression window retirement: %w", err)
	}
	return window, nil
}

func validateSuppressionTargetSQL(ctx context.Context, tx *sql.Tx, kind, id string) error {
	switch kind {
	case "fleet":
		if id != "" {
			return ErrInvalid
		}
	case "site":
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE id = $1) OR EXISTS (SELECT 1 FROM workspace_state WHERE singleton = true AND state_json->'sites' ? $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("validate suppression site: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	case "device":
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1) OR EXISTS (SELECT 1 FROM workspace_state WHERE singleton = true AND state_json->'devices' ? $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("validate suppression device: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	default:
		return ErrInvalid
	}
	return nil
}

type suppressionWindowCursor struct {
	Name string
	ID   string
}

func sortSuppressionWindows(items []SuppressionWindow) {
	sort.Slice(items, func(left, right int) bool {
		leftName := strings.ToLower(items[left].Name)
		rightName := strings.ToLower(items[right].Name)
		if leftName == rightName {
			return items[left].ID < items[right].ID
		}
		return leftName < rightName
	})
}

func suppressionWindowAfterCursor(item SuppressionWindow, cursor suppressionWindowCursor) bool {
	name := strings.ToLower(item.Name)
	return name > strings.ToLower(cursor.Name) || name == strings.ToLower(cursor.Name) && item.ID > cursor.ID
}

func suppressionWindowPage(items []SuppressionWindow, limit int) SuppressionWindowPage {
	page := SuppressionWindowPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeSuppressionWindowCursor(last)
	}
	return page
}

func encodeSuppressionWindowCursor(item SuppressionWindow) string {
	value := strings.ToLower(item.Name) + "\x00" + item.ID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSuppressionWindowCursor(value string) (*suppressionWindowCursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalid
	}
	parts := strings.SplitN(string(data), "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalid
	}
	return &suppressionWindowCursor{Name: parts[0], ID: parts[1]}, nil
}

func cloneSuppressionWindow(window SuppressionWindow) SuppressionWindow {
	window.Weekdays = append([]int(nil), window.Weekdays...)
	window.StartsAt = cloneTime(window.StartsAt)
	window.EndsAt = cloneTime(window.EndsAt)
	window.RetiredAt = cloneTime(window.RetiredAt)
	return window
}

func mapSuppressionSQLError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint") {
		return ErrConflict
	}
	return fmt.Errorf("suppression storage: %w", err)
}
