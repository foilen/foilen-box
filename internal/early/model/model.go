package model

import (
	"strings"
	"time"
)

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

const earlyTimestampLayout = "2006-01-02T15:04:05.000"

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

type Response struct {
	Error *ResponseError `json:"error"`
}

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
