package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"ali-mdm/server/internal/store"
)

func seedDefaultGroup(t *testing.T, e *testEnv) {
	t.Helper()
	err := e.st.UpsertGroup(&store.Group{
		ID: "default", Name: "Default",
		Config: `{"general":{"displayMode":"external_app"}}`, ConfigHash: "seed", ConfigVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The console sends the policy as a JSON object. The endpoint declared a string,
// so every save was rejected before anything examined it — and the only feedback
// was "bad body", which says nothing about what to change.
func TestUpdateGroupAcceptsConfigObject(t *testing.T) {
	e := newTestEnv(t)
	seedDefaultGroup(t, e)
	tok := e.login("admin@x.com", "adminpassword")

	rec, _ := e.do("PUT", "/api/v1/groups/default", tok, map[string]any{
		"config": map[string]any{
			"general": map[string]any{
				"displayMode": "website",
				"website":     map[string]any{"url": "https://example.com"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("saving a policy object should succeed, got %d (%s)", rec.Code, rec.Body.String())
	}

	g, err := e.st.GetGroup("default")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(g.Config), &saved); err != nil {
		t.Fatalf("stored config is not valid JSON: %v", err)
	}
	general, _ := saved["general"].(map[string]any)
	if general["displayMode"] != "website" {
		t.Fatalf("displayMode not persisted, got %v", saved)
	}
	if g.ConfigVersion != 2 {
		t.Fatalf("config version should advance so devices re-sync, got %d", g.ConfigVersion)
	}
	if g.ConfigHash == "seed" || g.ConfigHash == "" {
		t.Fatalf("config hash should be recomputed, got %q", g.ConfigHash)
	}
}

// The original contract — config as a JSON-encoded string — must keep working,
// so an older client is not broken by accepting the object form.
func TestUpdateGroupAcceptsConfigString(t *testing.T) {
	e := newTestEnv(t)
	seedDefaultGroup(t, e)
	tok := e.login("admin@x.com", "adminpassword")

	rec, _ := e.do("PUT", "/api/v1/groups/default", tok, map[string]any{
		"config": `{"general":{"displayMode":"website"}}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("saving a policy string should succeed, got %d (%s)", rec.Code, rec.Body.String())
	}
	g, _ := e.st.GetGroup("default")
	if g.Config == "" || g.ConfigVersion != 2 {
		t.Fatalf("string form did not persist: %+v", g)
	}
}

// A rejection should say what is wrong. "bad body" sent people hunting through
// the policy editor for a malformed field that was never the problem.
func TestUpdateGroupRejectionsAreExplained(t *testing.T) {
	e := newTestEnv(t)
	seedDefaultGroup(t, e)
	tok := e.login("admin@x.com", "adminpassword")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing config", map[string]any{"name": "Default"}},
		{"null config", map[string]any{"config": nil}},
		{"empty string config", map[string]any{"config": ""}},
	} {
		rec, body := e.do("PUT", "/api/v1/groups/default", tok, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", tc.name, rec.Code)
		}
		msg, _ := body["error"].(string)
		if msg == "" || msg == "bad body" {
			t.Fatalf("%s: expected an explanatory error, got %q", tc.name, msg)
		}
	}
}
