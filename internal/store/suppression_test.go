package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSuppressionWindowMemoryCRUDAndValidation(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	site, err := repository.CreateSite(ctx, Site{ID: "suppression-site", Name: "Suppression site"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, Device{ID: "suppression-device", SiteID: site.ID, DisplayName: "Suppression device"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.PutSuppressionWindow(ctx, SuppressionWindow{ID: "window-site", Name: "Site maintenance", TargetKind: "site", TargetID: site.ID, Enabled: true, Mode: SuppressionWindowRecurring, Timezone: "Europe/London", Weekdays: []int{5, 1}, StartLocal: "22:00", EndLocal: "23:00"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Weekdays[0] != 1 || created.Weekdays[1] != 5 || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created window = %+v", created)
	}
	updated := created
	updated.Name = "Updated maintenance"
	updated.Enabled = false
	updated, err = repository.PutSuppressionWindow(ctx, updated, 1)
	if err != nil || updated.Revision != 2 || updated.Enabled {
		t.Fatalf("updated window = %+v err=%v", updated, err)
	}
	if _, err := repository.PutSuppressionWindow(ctx, updated, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	if _, err := repository.PutSuppressionWindow(ctx, SuppressionWindow{ID: "window-other", Name: "Other maintenance", TargetKind: "fleet", Enabled: true, Mode: SuppressionWindowRecurring, Timezone: "UTC", Weekdays: []int{2}, StartLocal: "01:00", EndLocal: "02:00"}, 0); err != nil {
		t.Fatalf("second window: %v", err)
	}
	if _, err := repository.PutSuppressionWindow(ctx, SuppressionWindow{Name: "bad equal", TargetKind: "fleet", Enabled: true, Mode: SuppressionWindowRecurring, Timezone: "UTC", Weekdays: []int{1}, StartLocal: "09:00", EndLocal: "09:00"}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("equal boundary error = %v", err)
	}
	if _, err := repository.PutSuppressionWindow(ctx, SuppressionWindow{Name: "bad target", TargetKind: "device", TargetID: "missing", Enabled: true, Mode: SuppressionWindowRecurring, Timezone: "UTC", Weekdays: []int{1}, StartLocal: "09:00", EndLocal: "10:00"}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target error = %v", err)
	}
	start := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	if _, err := repository.PutSuppressionWindow(ctx, SuppressionWindow{Name: "too long", TargetKind: "device", TargetID: device.ID, Enabled: true, Mode: SuppressionWindowOneTime, StartsAt: &start, EndsAt: timePointer(start.Add(365*24*time.Hour + time.Minute))}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long one-time error = %v", err)
	}
	page, err := repository.ListSuppressionWindows(ctx, SuppressionWindowQuery{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first window page = %+v err=%v", page, err)
	}
	second, err := repository.ListSuppressionWindows(ctx, SuppressionWindowQuery{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(second.Items) != 1 {
		t.Fatalf("second window page = %+v err=%v", second, err)
	}
	retired, err := repository.RetireSuppressionWindow(ctx, created.ID, updated.Revision)
	if err != nil || retired.RetiredAt == nil || retired.Enabled || retired.Revision != 3 {
		t.Fatalf("retired window = %+v err=%v", retired, err)
	}
	page, err = repository.ListSuppressionWindows(ctx, SuppressionWindowQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("retired window remained listed = %+v err=%v", page, err)
	}
}

func TestSuppressionEpisodeMemoryIsUnionedAndClosedOnce(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	now := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	destination, err := repository.PutNotificationDestination(ctx, NotificationDestination{ID: "episode-destination", Name: "Episode destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true, SecretCiphertext: []byte("cipher"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	episode, err := repository.OpenSuppressionEpisode(ctx, destination.ID, "entity-1", "device-1", []string{"quiet:one"}, now)
	if err != nil || episode.Epoch != 1 || episode.StartedAt != now {
		t.Fatalf("episode = %+v err=%v", episode, err)
	}
	episode, err = repository.OpenSuppressionEpisode(ctx, destination.ID, "entity-1", "device-1", []string{"host_offline", "quiet:one"}, now.Add(time.Minute))
	if err != nil || len(episode.ReasonBits) != 2 || episode.StartedAt != now {
		t.Fatalf("unioned episode = %+v err=%v", episode, err)
	}
	if _, err := repository.CloseSuppressionEpisode(ctx, episode, nil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	open, err := repository.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("open episodes = %+v err=%v", open, err)
	}
	if _, err := repository.CloseSuppressionEpisode(ctx, episode, nil, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}
