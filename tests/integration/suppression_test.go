package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSQLSuppressionWindowsAndEpisodesPersistAcrossRestart(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	repository := store.NewSQL(db)
	now := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	siteID := "sql-suppression-site-" + store.NewID()
	deviceID := "sql-suppression-device-" + store.NewID()
	destinationID := "sql-suppression-destination-" + store.NewID()
	windowID := "sql-suppression-window-" + store.NewID()
	entityID := "sql-suppression-entity-" + store.NewID()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM suppression_episodes WHERE destination_id=$1 AND entity_id=$2`, destinationID, entityID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_deliveries WHERE destination_id=$1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM suppression_windows WHERE id=$1`, windowID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_destinations WHERE id=$1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `UPDATE workspace_state SET state_json = state_json #- ARRAY['devices', $1] #- ARRAY['sites', $2] WHERE singleton=true`, deviceID, siteID)
	})

	if _, err := repository.CreateSite(ctx, store.Site{ID: siteID, Name: "SQL suppression site"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDevice(ctx, store.Device{ID: deviceID, SiteID: siteID, DisplayName: "SQL suppression device"}); err != nil {
		t.Fatal(err)
	}
	destination, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: destinationID, Name: "SQL suppression destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sq********s", Enabled: true,
		SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	window, err := repository.PutSuppressionWindow(ctx, store.SuppressionWindow{
		ID: windowID, Name: "SQL maintenance", TargetKind: "device", TargetID: deviceID, Enabled: true,
		Mode: store.SuppressionWindowOneTime, StartsAt: sqlTimePointer(now.Add(-time.Minute)), EndsAt: sqlTimePointer(now.Add(time.Hour)),
	}, 0)
	if err != nil || window.Revision != 1 {
		t.Fatalf("SQL suppression window = %+v err=%v", window, err)
	}

	restarted := store.NewSQL(db)
	restarted.SetClock(func() time.Time { return now })
	loaded, err := restarted.GetSuppressionWindow(ctx, windowID)
	if err != nil || loaded.TargetID != deviceID || loaded.Mode != store.SuppressionWindowOneTime {
		t.Fatalf("restarted suppression window = %+v err=%v", loaded, err)
	}
	page, err := restarted.ListSuppressionWindows(ctx, store.SuppressionWindowQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != windowID {
		t.Fatalf("SQL suppression window list = %+v err=%v", page.Items, err)
	}

	episode, err := restarted.OpenSuppressionEpisode(ctx, destination.ID, entityID, deviceID, []string{"quiet-window"}, now)
	if err != nil || episode.Epoch != 1 {
		t.Fatalf("SQL suppression episode = %+v err=%v", episode, err)
	}
	open, err := restarted.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(open) != 1 || open[0].Epoch != episode.Epoch {
		t.Fatalf("SQL open suppression episodes = %+v err=%v", open, err)
	}

	summary := store.NotificationDelivery{
		ID: "sql-suppression-summary-" + store.NewID(), DestinationID: destination.ID, DestinationRevision: destination.Revision,
		SummaryKey: "suppression:" + entityID + ":1", Status: store.NotificationDeliveryQueued, Payload: []byte(`{"title":"summary","message":"active incident","priority":3}`),
		ExpiresAt: now.Add(24 * time.Hour), NextAttemptAt: sqlTimePointer(now), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := restarted.CloseSuppressionEpisode(ctx, episode, &summary, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CloseSuppressionEpisode(ctx, episode, &summary, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	open, err = restarted.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("SQL episodes after close = %+v err=%v", open, err)
	}
	queued, err := restarted.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{DestinationID: destination.ID, Status: store.NotificationDeliveryQueued, Limit: 10})
	if err != nil || len(queued.Items) != 1 || queued.Items[0].SummaryKey != summary.SummaryKey {
		t.Fatalf("SQL suppression summary queue = %+v err=%v", queued.Items, err)
	}

	if _, err := restarted.PutSuppressionWindow(ctx, store.SuppressionWindow{ID: windowID, Name: "SQL changed", TargetKind: "device", TargetID: deviceID, Enabled: true, Mode: store.SuppressionWindowOneTime, StartsAt: sqlTimePointer(now), EndsAt: sqlTimePointer(now.Add(time.Hour))}, loaded.Revision+1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale SQL suppression update = %v", err)
	}
}

func sqlTimePointer(value time.Time) *time.Time {
	return &value
}
