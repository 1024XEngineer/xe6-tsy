package webapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

const (
	defaultLimit = 20
	maximumLimit = 100
)

func parseParticipantsQuery(request *http.Request) (recordsv1.ListParticipantsQuery, error) {
	values := request.URL.Query()
	if err := allowOnly(values, "cursor", "limit"); err != nil {
		return recordsv1.ListParticipantsQuery{}, err
	}
	page, err := parsePage(values)
	if err != nil {
		return recordsv1.ListParticipantsQuery{}, err
	}
	return recordsv1.ListParticipantsQuery{Cursor: page.cursor, Limit: page.limit}, nil
}

func parseSessionTurnsQuery(request *http.Request) (recordsv1.ListTurnsQuery, error) {
	values := request.URL.Query()
	if err := allowOnly(values, "cursor", "limit", "participant_id", "speaker_code", "attribution_status", "source_language", "target_language"); err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	page, err := parsePage(values)
	if err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	status, err := parseAttributionStatus(values)
	if err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	return recordsv1.ListTurnsQuery{
		Cursor:            page.cursor,
		Limit:             page.limit,
		ParticipantID:     values.Get("participant_id"),
		SpeakerCode:       values.Get("speaker_code"),
		AttributionStatus: status,
		SourceLanguage:    values.Get("source_language"),
		TargetLanguage:    values.Get("target_language"),
	}, nil
}

func parseHistoryQuery(request *http.Request) (recordsv1.ListTurnsQuery, error) {
	values := request.URL.Query()
	if err := allowOnly(values, "cursor", "limit", "session_id", "participant_id", "source_language", "target_language", "created_from", "created_to"); err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	page, err := parsePage(values)
	if err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	createdFrom, err := parseTime(values, "created_from")
	if err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	createdTo, err := parseTime(values, "created_to")
	if err != nil {
		return recordsv1.ListTurnsQuery{}, err
	}
	if createdFrom != nil && createdTo != nil && createdFrom.After(*createdTo) {
		return recordsv1.ListTurnsQuery{}, errors.New("created_from must not be after created_to")
	}
	return recordsv1.ListTurnsQuery{
		Cursor:         page.cursor,
		Limit:          page.limit,
		SessionID:      values.Get("session_id"),
		ParticipantID:  values.Get("participant_id"),
		SourceLanguage: values.Get("source_language"),
		TargetLanguage: values.Get("target_language"),
		CreatedFrom:    createdFrom,
		CreatedTo:      createdTo,
	}, nil
}

type page struct {
	cursor string
	limit  int
}

func parsePage(values url.Values) (page, error) {
	limit := defaultLimit
	if values.Has("limit") {
		parsed, err := strconv.Atoi(values.Get("limit"))
		if err != nil || parsed < 1 || parsed > maximumLimit {
			return page{}, fmt.Errorf("limit must be between 1 and %d", maximumLimit)
		}
		limit = parsed
	}
	return page{cursor: values.Get("cursor"), limit: limit}, nil
}

func parseAttributionStatus(values url.Values) (recordsv1.AttributionStatus, error) {
	if !values.Has("attribution_status") {
		return "", nil
	}
	status := recordsv1.AttributionStatus(values.Get("attribution_status"))
	switch status {
	case recordsv1.AttributionPending, recordsv1.AttributionProvisional, recordsv1.AttributionConfirmed, recordsv1.AttributionCorrected:
		return status, nil
	default:
		return "", errors.New("invalid attribution_status")
	}
}

func parseTime(values url.Values, key string) (*time.Time, error) {
	if !values.Has(key) {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, values.Get(key))
	if err != nil {
		return nil, fmt.Errorf("%s must use RFC3339", key)
	}
	return &parsed, nil
}

func allowOnly(values url.Values, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowedSet[key]; !ok {
			return fmt.Errorf("query parameter %q is not allowed", key)
		}
		if len(entries) != 1 {
			return fmt.Errorf("query parameter %q must occur once", key)
		}
	}
	return nil
}
