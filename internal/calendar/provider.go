// Package calendar exposes read-only calendar capabilities, without agent logic.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type ListOptions struct {
	TimeMin   time.Time `json:"time_min"`
	TimeMax   time.Time `json:"time_max"`
	PageToken string    `json:"page_token,omitempty"`
}

type Event struct {
	ID           string `json:"id"`
	Summary      string `json:"summary"`
	Start        string `json:"start"`
	End          string `json:"end"`
	AllDay       bool   `json:"all_day"`
	Transparency string `json:"transparency,omitempty"`
	SelfResponse string `json:"self_response,omitempty"`
}

type Page struct {
	Events        []Event `json:"events"`
	Timezone      string  `json:"timezone"`
	NextPageToken string  `json:"next_page_token,omitempty"`
}

type Reader interface {
	ListEvents(context.Context, ListOptions) (Page, error)
}

type TokenStore interface {
	Load() (*oauth2.Token, error)
	Save(*oauth2.Token) error
}

type Provider struct {
	config *oauth2.Config
	tokens TokenStore
}

func NewProvider(config *oauth2.Config, tokens TokenStore) *Provider {
	return &Provider{config: config, tokens: tokens}
}

func (p *Provider) ListEvents(ctx context.Context, opts ListOptions) (Page, error) {
	if opts.TimeMin.IsZero() || !opts.TimeMax.After(opts.TimeMin) || opts.TimeMax.Sub(opts.TimeMin) > 31*24*time.Hour {
		return Page{}, errors.New("calendar interval must be positive and at most 31 days")
	}
	token, err := p.tokens.Load()
	if err != nil {
		return Page{}, errors.New("Google Calendar is not connected; authorize calendar read access")
	}
	source := &savingSource{base: p.config.TokenSource(ctx, token), store: p.tokens, latest: token}
	service, err := googlecalendar.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(ctx, source)))
	if err != nil {
		return Page{}, err
	}
	result, err := service.Events.List("primary").TimeMin(opts.TimeMin.Format(time.RFC3339)).TimeMax(opts.TimeMax.Format(time.RFC3339)).
		SingleEvents(true).OrderBy("startTime").MaxResults(100).PageToken(opts.PageToken).Context(ctx).Do()
	if err != nil {
		return Page{}, fmt.Errorf("read Google Calendar (read scope and Calendar API must be enabled): %w", err)
	}
	page := Page{Events: make([]Event, 0, len(result.Items)), Timezone: result.TimeZone, NextPageToken: result.NextPageToken}
	for _, item := range result.Items {
		if item.Status == "cancelled" || item.Start == nil || item.End == nil {
			continue
		}
		event := Event{ID: item.Id, Summary: item.Summary, Start: item.Start.DateTime, End: item.End.DateTime, Transparency: item.Transparency}
		if item.Start.Date != "" {
			event.AllDay, event.Start, event.End = true, item.Start.Date, item.End.Date
		}
		for _, attendee := range item.Attendees {
			if attendee.Self {
				event.SelfResponse = attendee.ResponseStatus
				break
			}
		}
		page.Events = append(page.Events, event)
	}
	return page, nil
}

type savingSource struct {
	base   oauth2.TokenSource
	store  TokenStore
	latest *oauth2.Token
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	token, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	if s.latest.AccessToken != token.AccessToken || s.latest.RefreshToken != token.RefreshToken {
		if err := s.store.Save(token); err != nil {
			return nil, err
		}
		s.latest = token
	}
	return token, nil
}
