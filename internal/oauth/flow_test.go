package oauth

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/gmail/v1"
)

func TestOAuthScopesAreIncrementalAndCalendarIsReadOnly(t *testing.T) {
	config := &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: "https://example.com/auth"}, Scopes: []string{"original"}}
	flow := NewFlow(config, NewFileTokenStore(filepath.Join(t.TempDir(), "tokens.json")))
	for _, test := range []struct{ calendar, send bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		var authURL string
		var err error
		if test.calendar {
			authURL, err = flow.StartWithCalendar(test.send)
		} else {
			authURL, err = flow.Start(test.send)
		}
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(authURL)
		scopes := strings.Fields(parsed.Query().Get("scope"))
		want := 1
		if test.send {
			want++
		}
		if test.calendar {
			want++
		}
		if len(scopes) != want || scopes[0] != gmail.GmailReadonlyScope || strings.Contains(parsed.Query().Get("scope"), calendar.CalendarEventsScope+" ") {
			t.Fatalf("incorrect scopes: %v", scopes)
		}
		if test.calendar && scopes[len(scopes)-1] != calendar.CalendarEventsReadonlyScope {
			t.Fatal("calendar write scope requested")
		}
		if parsed.Query().Get("state") == "" || parsed.Query().Get("include_granted_scopes") != "true" {
			t.Fatal("missing state or incremental authorization")
		}
	}
	if config.Scopes[0] != "original" {
		t.Fatal("shared OAuth configuration was mutated")
	}
}
