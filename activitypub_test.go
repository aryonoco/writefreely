package writefreely

import (
	"strings"
	"testing"
	"time"

	"github.com/writeas/web-core/activitystreams"
)

var actorTestTable = []struct {
	Name string
	Resp []byte
}{
	{
		"Context as a string",
		[]byte(`{"@context":"https://www.w3.org/ns/activitystreams"}`),
	},
	{
		"Context as a list",
		[]byte(`{"@context":["one string", "two strings"]}`),
	},
}

func TestUnmarshalActor(t *testing.T) {
	for _, tc := range actorTestTable {
		actor := activitystreams.Person{}
		err := unmarshalActor(tc.Resp, &actor)
		if err != nil {
			t.Errorf("%s failed with error %s", tc.Name, err)
		}
	}
}

func TestUnmarshalActorReadsURLAndIconForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		resp       string
		wantInbox  string
		wantShared string
		wantURL    string
		wantIcon   string
	}{
		{
			name: "PeerTube-style icon array",
			resp: `{"type":"Person","id":"https://tube.example/accounts/alice",
				"inbox":"https://tube.example/accounts/alice/inbox",
				"endpoints":{"sharedInbox":"https://tube.example/inbox"},
				"url":"https://tube.example/a/alice",
				"icon":[{"type":"Image","mediaType":"image/png","width":48,"url":"https://tube.example/small.png"},
					{"type":"Image","mediaType":"image/png","width":120,"url":"https://tube.example/large.png"}]}`,
			wantInbox:  "https://tube.example/accounts/alice/inbox",
			wantShared: "https://tube.example/inbox",
			wantURL:    "https://tube.example/a/alice",
			wantIcon:   "https://tube.example/small.png",
		},
		{
			name: "Hubzilla-style url Link object",
			resp: `{"type":"Person","id":"https://hub.example/channel/alice",
				"inbox":"https://hub.example/inbox/alice",
				"endpoints":{"sharedInbox":"https://hub.example/inbox"},
				"url":{"type":"Link","mediaType":"text/html","href":"https://hub.example/channel/alice"},
				"icon":{"type":"Image","mediaType":"image/jpeg","url":"https://hub.example/photo/alice.jpg"}}`,
			wantInbox:  "https://hub.example/inbox/alice",
			wantShared: "https://hub.example/inbox",
			wantURL:    "https://hub.example/channel/alice",
			wantIcon:   "https://hub.example/photo/alice.jpg",
		},
		{
			name: "url array of Links",
			resp: `{"type":"Person","id":"https://social.example/users/alice",
				"inbox":"https://social.example/users/alice/inbox",
				"endpoints":{"sharedInbox":"https://social.example/inbox"},
				"url":[{"type":"Link","mediaType":"text/html","href":"https://social.example/@alice"},
					{"type":"Link","mediaType":"application/activity+json","href":"https://social.example/users/alice"}]}`,
			wantInbox:  "https://social.example/users/alice/inbox",
			wantShared: "https://social.example/inbox",
			wantURL:    "https://social.example/@alice",
		},
		{
			name: "url array of strings",
			resp: `{"type":"Person","id":"https://social.example/users/alice",
				"inbox":"https://social.example/users/alice/inbox",
				"endpoints":{"sharedInbox":"https://social.example/inbox"},
				"url":["https://social.example/@alice","https://social.example/users/alice"]}`,
			wantInbox:  "https://social.example/users/alice/inbox",
			wantShared: "https://social.example/inbox",
			wantURL:    "https://social.example/@alice",
		},
		{
			name: "url nested array",
			resp: `{"type":"Person","id":"https://social.example/users/alice",
				"inbox":"https://social.example/users/alice/inbox",
				"endpoints":{"sharedInbox":"https://social.example/inbox"},
				"url":[["https://social.example/@alice"]]}`,
			wantInbox:  "https://social.example/users/alice/inbox",
			wantShared: "https://social.example/inbox",
			wantURL:    "",
		},
		{
			name: "icon nested array",
			resp: `{"type":"Person","id":"https://social.example/users/alice",
				"inbox":"https://social.example/users/alice/inbox",
				"endpoints":{"sharedInbox":"https://social.example/inbox"},
				"url":"https://social.example/@alice",
				"icon":[[{"type":"Image","mediaType":"image/png","url":"https://social.example/alice.png"}]]}`,
			wantInbox:  "https://social.example/users/alice/inbox",
			wantShared: "https://social.example/inbox",
			wantURL:    "https://social.example/@alice",
			wantIcon:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actor := activitystreams.Person{}
			if err := unmarshalActor([]byte(tt.resp), &actor); err != nil {
				t.Fatalf("unmarshalActor: %v", err)
			}
			if actor.Inbox != tt.wantInbox {
				t.Errorf("Inbox = %q, want %q", actor.Inbox, tt.wantInbox)
			}
			if actor.Endpoints.SharedInbox != tt.wantShared {
				t.Errorf("Endpoints.SharedInbox = %q, want %q", actor.Endpoints.SharedInbox, tt.wantShared)
			}
			if actor.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", actor.URL, tt.wantURL)
			}
			if actor.Icon.URL != tt.wantIcon {
				t.Errorf("Icon.URL = %q, want %q", actor.Icon.URL, tt.wantIcon)
			}
		})
	}
}

func TestUnmarshalActorReadsDeeplyNestedURLQuickly(t *testing.T) { //nolint:paralleltest // measures elapsed time, so it runs alone
	const depth = 9000
	resp := `{"type":"Person","inbox":"https://social.example/users/alice/inbox","url":` +
		strings.Repeat("[", depth) + `"https://social.example/@alice"` + strings.Repeat("]", depth) + `}`

	actor := activitystreams.Person{}
	start := time.Now()
	err := unmarshalActor([]byte(resp), &actor)
	elapsed := time.Since(start)
	t.Logf("%d levels, %d bytes: %v", depth, len(resp), elapsed)

	if err != nil {
		t.Fatalf("unmarshalActor: %v", err)
	}
	if want := "https://social.example/users/alice/inbox"; actor.Inbox != want {
		t.Errorf("Inbox = %q, want %q", actor.Inbox, want)
	}
	if actor.URL != "" {
		t.Errorf("URL = %q, want \"\"", actor.URL)
	}
	// A single pass over the document takes about a millisecond; the bound
	// is generous so that only cost growing with the nesting depth trips it.
	if elapsed > time.Second {
		t.Errorf("unmarshalActor took %v, want under 1s", elapsed)
	}
}
