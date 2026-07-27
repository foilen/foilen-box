package aggregate

import (
	"testing"
	"time"

	"foilen-box/internal/early/model"
)

type fakeEarlyService struct {
	entries []model.TimeEntry
	deleted []string
}

func (f *fakeEarlyService) Connect(cfg model.ConfigEarly) error { return nil }

func (f *fakeEarlyService) TimeEntries(from, to time.Time) (*model.TimeEntriesResponse, error) {
	return &model.TimeEntriesResponse{TimeEntries: f.entries}, nil
}

func (f *fakeEarlyService) TimeEntryDelete(id string) (*model.Response, error) {
	f.deleted = append(f.deleted, id)
	return &model.Response{}, nil
}

type fakeConfigService struct{}

func (fakeConfigService) Load() model.ConfigEarly { return model.ConfigEarly{} }

func entry(id, activity string, tags []string, start time.Time, seconds int64) model.TimeEntry {
	var noteTags []model.Tag
	for _, t := range tags {
		noteTags = append(noteTags, model.Tag{Label: t})
	}
	return model.TimeEntry{
		ID:       id,
		Activity: model.Activity{Name: activity},
		Duration: model.Duration{StartedAt: model.FlexTime(start), StoppedAt: model.FlexTime(start.Add(time.Duration(seconds) * time.Second))},
		Note:     model.Note{Tags: noteTags},
	}
}

func TestAggregate(t *testing.T) {
	day := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	svc := &fakeEarlyService{entries: []model.TimeEntry{
		entry("1", "Coding", []string{"backend"}, day, 3600),
		entry("2", "Coding", nil, day, 1800),
		entry("3", "Meetings", []string{"standup", "planning"}, day, 900),
	}}

	agg := New(svc, fakeConfigService{})
	result, err := agg.Aggregate()
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}

	if result.TotalDurationInSec != 3600+1800+900 {
		t.Errorf("TotalDurationInSec = %d, want %d", result.TotalDurationInSec, 3600+1800+900)
	}

	if got := result.DurationInSecByActivity["Coding"]; got != 5400 {
		t.Errorf("DurationInSecByActivity[Coding] = %d, want 5400", got)
	}
	if got := result.DurationInSecByActivity["Meetings"]; got != 900 {
		t.Errorf("DurationInSecByActivity[Meetings] = %d, want 900", got)
	}

	dayKey := "Coding / 2025-06-01"
	if got := result.DurationInSecByActivityDay[dayKey]; got != 5400 {
		t.Errorf("DurationInSecByActivityDay[%q] = %d, want 5400", dayKey, got)
	}

	// entry 2 has no tags -> NO_TAG bucket
	if got := result.DurationInSecByActivityTag["Coding / NO_TAG"]; got != 1800 {
		t.Errorf("DurationInSecByActivityTag[Coding / NO_TAG] = %d, want 1800", got)
	}
	if got := result.DurationInSecByActivityTag["Coding / backend"]; got != 3600 {
		t.Errorf("DurationInSecByActivityTag[Coding / backend] = %d, want 3600", got)
	}
	// entry 3 has 2 tags -> contributes to both tag buckets
	if got := result.DurationInSecByActivityTag["Meetings / standup"]; got != 900 {
		t.Errorf("DurationInSecByActivityTag[Meetings / standup] = %d, want 900", got)
	}
	if got := result.DurationInSecByActivityTag["Meetings / planning"]; got != 900 {
		t.Errorf("DurationInSecByActivityTag[Meetings / planning] = %d, want 900", got)
	}

	wantActivities := []string{"Coding", "Meetings"}
	if len(result.ActivityNames) != len(wantActivities) {
		t.Fatalf("ActivityNames = %v, want %v", result.ActivityNames, wantActivities)
	}
	for i, name := range wantActivities {
		if result.ActivityNames[i] != name {
			t.Errorf("ActivityNames[%d] = %q, want %q", i, result.ActivityNames[i], name)
		}
	}
}

func TestAggregateSkipsIncompleteEntries(t *testing.T) {
	svc := &fakeEarlyService{entries: []model.TimeEntry{
		{ID: "1", Activity: model.Activity{Name: "Coding"}}, // zero-value Duration
	}}
	agg := New(svc, fakeConfigService{})
	result, err := agg.Aggregate()
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if result.TotalDurationInSec != 0 {
		t.Errorf("TotalDurationInSec = %d, want 0", result.TotalDurationInSec)
	}
}

func TestDeleteByActivity(t *testing.T) {
	day := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	svc := &fakeEarlyService{entries: []model.TimeEntry{
		entry("1", "Coding", nil, day, 60),
		entry("2", "Meetings", nil, day, 60),
		entry("3", "Coding", nil, day, 60),
	}}

	agg := New(svc, fakeConfigService{})
	count, err := agg.DeleteByActivity("Coding")
	if err != nil {
		t.Fatalf("DeleteByActivity() error = %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if len(svc.deleted) != 2 || svc.deleted[0] != "1" || svc.deleted[1] != "3" {
		t.Errorf("deleted = %v, want [1 3]", svc.deleted)
	}
}
