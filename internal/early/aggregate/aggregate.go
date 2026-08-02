// Package aggregate fetches Early time entries and aggregates their
// durations by activity/day/tag, or deletes entries by activity name.
package aggregate

import (
	"fmt"
	"sort"
	"time"

	"foilen-box/internal/early/model"
)

// EarlyService is the subset of the Early API client this package needs.
type EarlyService interface {
	Connect(cfg model.ConfigEarly) error
	TimeEntries(from, to time.Time) (*model.TimeEntriesResponse, error)
	TimeEntryDelete(id string) (*model.Response, error)
}

// ConfigService is the subset of the config service this package needs.
type ConfigService interface {
	Load() model.ConfigEarly
}

const dayLayout = "2006-01-02"

// Service orchestrates connect+fetch+aggregate/delete against Early.
type Service struct {
	earlyService  EarlyService
	configService ConfigService
}

func New(earlyService EarlyService, configService ConfigService) *Service {
	return &Service{earlyService: earlyService, configService: configService}
}

// Aggregate connects, fetches time entries for the past-to-next year window,
// and aggregates their durations.
func (s *Service) Aggregate() (*model.AggregateResult, error) {
	response, err := s.connectAndFetch()
	if err != nil {
		return nil, err
	}

	result := model.NewAggregateResult()
	var total int64
	activitySet := map[string]struct{}{}

	for _, entry := range response.TimeEntries {
		if entry.Duration.StartedAt.IsZero() || entry.Duration.StoppedAt.IsZero() {
			continue
		}
		durationInSec := int64(entry.Duration.StoppedAt.Sub(time.Time(entry.Duration.StartedAt)).Seconds())
		total += durationInSec

		activityName := entry.Activity.Name
		if activityName == "" {
			activityName = "Unknown"
		}
		activitySet[activityName] = struct{}{}

		day := entry.Duration.StartedAt.Format(dayLayout)

		addToMap(result.DurationInSecByActivityDay, activityName+" / "+day, durationInSec)
		addToMap(result.DurationInSecByActivity, activityName, durationInSec)

		var tags []string
		for _, t := range entry.Note.Tags {
			if t.Label != "" {
				tags = append(tags, t.Label)
			}
		}

		if len(tags) == 0 {
			tags = []string{"NO_TAG"}
		}
		for _, tag := range tags {
			addToMap(result.DurationInSecByActivityDayTag, activityName+" / "+day+" / "+tag, durationInSec)
			addToMap(result.DurationInSecByActivityTag, activityName+" / "+tag, durationInSec)
		}
	}

	result.TotalDurationInSec = total
	activityNames := make([]string, 0, len(activitySet))
	for name := range activitySet {
		activityNames = append(activityNames, name)
	}
	sort.Strings(activityNames)
	result.ActivityNames = activityNames

	return result, nil
}

// DeleteByActivity connects, fetches time entries for the past-to-next year
// window, and deletes every entry matching activityName exactly. Returns the
// count of deleted entries.
func (s *Service) DeleteByActivity(activityName string) (int, error) {
	response, err := s.connectAndFetch()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range response.TimeEntries {
		if entry.Activity.Name != activityName {
			continue
		}
		deleteResponse, err := s.earlyService.TimeEntryDelete(entry.ID)
		if err != nil {
			return count, err
		}
		if !deleteResponse.IsSuccess() {
			return count, fmt.Errorf("failed to delete entry %s: %s", entry.ID, deleteResponse.Error.Message)
		}
		count++
	}
	return count, nil
}

func (s *Service) connectAndFetch() (*model.TimeEntriesResponse, error) {
	cfg := s.configService.Load()
	if err := s.earlyService.Connect(cfg); err != nil {
		return nil, err
	}

	from, to := lastYearWindow()
	response, err := s.earlyService.TimeEntries(from, to)
	if err != nil {
		return nil, err
	}
	if !response.IsSuccess() {
		return nil, fmt.Errorf("failed to fetch time entries: %s", response.Error.Message)
	}
	return response, nil
}

// lastYearWindow mirrors fetchLastYear(): [now-1y, now+1y].
func lastYearWindow() (from, to time.Time) {
	from = time.Now().AddDate(-1, 0, 0)
	to = from.AddDate(2, 0, 0)
	return from, to
}

func addToMap(m map[string]int64, key string, amount int64) {
	m[key] += amount
}
