package core_test

import (
	"slices"
	"testing"
	"time"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/store"
)

// A manifest that names notify (routing `to: notify`, a watch rule `notify: notify`) still loads:
// the lines come back as warnings (check, team up and found print them) and have no effect, because
// only the engine writes to notify.
func TestNotifyLinesLoadWithNoEffect(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Unix(1_800_000_000, 0)
	var sunk []string
	e := core.New(db, core.WithClock(func() time.Time { return now }), core.WithNotifySink(func(id string) { sunk = append(sunk, id) }))
	const man = `template: nl
roles: {lead: {tools: [send, inbox, who, agent]}, worker: {tools: [send, inbox, who, agent]}}
routing:
  - {from: lead, to: notify, allow: true}
  - {from: lead, to: worker, allow: true}
timers:
  - {on: worker, notify: notify, silent_for: 10m}
`
	warns, err := core.CheckManifest(man)
	if err != nil || len(warns) != 2 {
		t.Fatalf("CheckManifest = %q, %v; want the two notify lines as warnings", warns, err)
	}
	team, err := e.TeamUp(ctx, core.TeamUpArgs{Manifest: man, Cwd: t.TempDir()})
	if err != nil || !slices.Equal(team.Warnings, warns) {
		t.Fatalf("team up: warnings %q, %v; want %q", team.Warnings, err, warns)
	}
	join := func(name, role string) core.Caller {
		j, err := e.Join(ctx, core.JoinArgs{Team: team.ID, Role: role, Name: name, Cwd: team.RootCwd})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := e.Authenticate(ctx, j.ID, j.Token)
		return c
	}
	lead, w := join("lead", "lead"), join("w", "worker")
	if _, err := e.Send(ctx, lead, core.SendArgs{To: core.AddrNotify, Body: "hello"}); rule(err) != "routing/routing.notify" {
		t.Fatalf("send to notify: %v; want it refused though routing allows it", err)
	}
	if err := e.Presence(ctx, w, core.PresenceArgs{Event: core.PresenceAgentStart}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	if n, err := e.Watch(ctx); err != nil || n != 0 || len(sunk) != 0 {
		t.Fatalf("watch fired %d (%v), sink %v; want a rule notifying notify never to fire", n, err, sunk)
	}
}
