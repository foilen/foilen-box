// Package model holds the data types exchanged with the Early.app API and
// the local Early configuration. encoding/json ignores unknown fields by
// default, matching the Java @JsonIgnoreProperties(ignoreUnknown = true)
// behavior these types used to have.
package model

import (
	"strings"
	"time"
)

// ConfigEarly is the locally persisted Early API configuration.
type ConfigEarly struct {
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
}

type Activity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	FolderID string `json:"folderId"`
}

type Duration struct {
	StartedAt FlexTime `json:"startedAt"`
	StoppedAt FlexTime `json:"stoppedAt"`
}

// earlyTimestampLayout matches the offset-less, millisecond-precision UTC
// timestamps Early sends (e.g. "2026-06-30T01:19:03.970").
const earlyTimestampLayout = "2006-01-02T15:04:05.000"

// FlexTime is a time.Time that accepts both RFC3339 timestamps and Early's
// offset-less UTC timestamps, and treats an empty string as the zero time
// (sent for the stoppedAt field of a time entry that is still running).
type FlexTime time.Time

func (t FlexTime) IsZero() bool {
	return time.Time(t).IsZero()
}

func (t FlexTime) Sub(u time.Time) time.Duration {
	return time.Time(t).Sub(u)
}

func (t FlexTime) Format(layout string) string {
	return time.Time(t).Format(layout)
}

func (t *FlexTime) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		*t = FlexTime(time.Time{})
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, s); err == nil {
		*t = FlexTime(parsed)
		return nil
	}
	parsed, err := time.ParseInLocation(earlyTimestampLayout, s, time.UTC)
	if err != nil {
		return err
	}
	*t = FlexTime(parsed)
	return nil
}

// Mention is currently unused by the Early API responses this app reads.
type Mention struct{}

type Tag struct {
	ID       *int   `json:"id"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	Scope    string `json:"scope"`
	FolderID string `json:"folderId"`
}

type Note struct {
	Text     string    `json:"text"`
	Tags     []Tag     `json:"tags"`
	Mentions []Mention `json:"mentions"`
}

type ResponseError struct {
	Message string `json:"message"`
}

// Response is the common envelope for Early API responses.
type Response struct {
	Error *ResponseError `json:"error"`
}

// IsSuccess mirrors Java's Response.isSuccess(): no error means success.
func (r Response) IsSuccess() bool {
	return r.Error == nil
}

type SignInRequest struct {
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
}

type SignInResponse struct {
	Response
	Token string `json:"token"`
}

type TimeEntry struct {
	ID       string   `json:"id"`
	Activity Activity `json:"activity"`
	Duration Duration `json:"duration"`
	Note     Note     `json:"note"`
}

type TimeEntriesResponse struct {
	Response
	TimeEntries []TimeEntry `json:"timeEntries"`
}

// AggregateResult is computed locally, not part of the Early API wire format.
// Maps are plain Go maps (unordered); callers must sort keys themselves when
// a stable/sorted iteration order is needed (e.g. for report formatting),
// since Go has no built-in TreeMap equivalent.
type AggregateResult struct {
	DurationInSecByActivityDayTag map[string]int64
	DurationInSecByActivityTag    map[string]int64
	DurationInSecByActivityDay    map[string]int64
	DurationInSecByActivity       map[string]int64
	TotalDurationInSec            int64
	ActivityNames                 []string
}

func NewAggregateResult() *AggregateResult {
	return &AggregateResult{
		DurationInSecByActivityDayTag: map[string]int64{},
		DurationInSecByActivityTag:    map[string]int64{},
		DurationInSecByActivityDay:    map[string]int64{},
		DurationInSecByActivity:       map[string]int64{},
	}
}
