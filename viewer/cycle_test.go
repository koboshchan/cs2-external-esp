package main

import "testing"

func makePlayers(alive ...bool) []Player {
	players := make([]Player, len(alive))
	for i, a := range alive {
		players[i].Index = int8(i)
		if a {
			players[i].Flags |= playerAlive
		}
	}
	return players
}

func TestNextPlayerWraps(t *testing.T) {
	players := makePlayers(true, true, true)

	seen := []int8{}
	current := (*Player)(nil)

	for i := 0; i < 4; i++ {
		next := nextPlayerIndex(players, current)
		seen = append(seen, int8(next))
		current = &players[next]
	}

	want := []int8{0, 1, 2, 0}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("cycle = %v, want %v", seen, want)
		}
	}
}

func TestNextPlayerSkipsDead(t *testing.T) {
	players := makePlayers(true, false, true, false)

	current := &players[0]
	if got := nextPlayerIndex(players, current); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}

	current = &players[2]
	if got := nextPlayerIndex(players, current); got != 0 {
		t.Fatalf("wrap got %d, want 0", got)
	}
}

func TestNextPlayerAllDead(t *testing.T) {
	players := makePlayers(false, false)

	current := &players[0]
	if got := nextPlayerIndex(players, current); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	if got := nextPlayerIndex(players, &players[1]); got != 0 {
		t.Fatalf("wrap got %d, want 0", got)
	}
}

func TestNextPlayerEmpty(t *testing.T) {
	if got := nextPlayerIndex(nil, nil); got != -1 {
		t.Fatalf("got %d, want -1", got)
	}
}
