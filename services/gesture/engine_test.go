package gesture

import (
	"testing"
	"time"

	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

func testEngine() *Engine {
	return NewEngine(utils.SpotifyConfig{
		QuickPauseMax:     4 * time.Second,
		EdgeFastResumeMax: 2800 * time.Millisecond,
	}, nil, nil, nil)
}

func TestEdgeResumeQualifies_confirmed(t *testing.T) {
	e := testEngine()
	prev := spotify.PollState{PauseConfirmed: true}
	if !e.edgeResumeQualifies(prev, 5*time.Second, 0) {
		t.Fatal("confirmed pause should always qualify")
	}
}

func TestEdgeResumeQualifies_fastUnconfirmed(t *testing.T) {
	e := testEngine()
	prev := spotify.PollState{}
	if !e.edgeResumeQualifies(prev, 2*time.Second, 0) {
		t.Fatal("short unconfirmed pause should qualify")
	}
}

func TestEdgeResumeQualifies_progressResumeAtCeiling(t *testing.T) {
	e := testEngine()
	prev := spotify.PollState{}
	// Logged pause_ms=4000 can be 4.0–4.5s; playhead moved = real resume between polls.
	if !e.edgeResumeQualifies(prev, 4001*time.Millisecond, 1927) {
		t.Fatal("unconfirmed resume with progress advance at quick-pause ceiling should qualify")
	}
}

func TestEdgeResumeQualifies_glitchHoldNoProgress(t *testing.T) {
	e := testEngine()
	prev := spotify.PollState{}
	if e.edgeResumeQualifies(prev, 3500*time.Millisecond, 0) {
		t.Fatal("long unconfirmed hold without progress should not qualify")
	}
}

func TestQuickPauseMaxEffective(t *testing.T) {
	e := testEngine()
	got := e.quickPauseMaxEffective()
	want := 4500 * time.Millisecond
	if got != want {
		t.Fatalf("effective max = %v, want %v", got, want)
	}
}
