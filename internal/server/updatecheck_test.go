package server

import (
	"context"
	"testing"
	"time"
)

func TestLocalBuildSkipsDailyReleaseCheck(t *testing.T) {
	calls := 0
	s := &server{
		dir:     t.TempDir(),
		version: "local-abc123",
		latest: func(context.Context) (string, error) {
			calls++
			return "v9.9.9", nil
		},
		settings: Settings{UpdateCheck: true},
	}
	s.checkUpdate(context.Background(), time.Now().Add(48*time.Hour))
	if calls != 0 {
		t.Fatalf("local build release calls = %d; want none", calls)
	}
	if !DevBuild("local-abc123") || DevBuild("v0.7.0") {
		t.Fatal("DevBuild classification does not distinguish local and release versions")
	}
}
