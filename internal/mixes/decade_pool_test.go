package mixes

import (
	"reflect"
	"strconv"
	"testing"
)

// decadeTracks returns count tracks from the given year, each by its own
// artist so the per-artist cap never bites, each played plays times.
func decadeTracks(year, count, plays int) []Track {
	out := make([]Track, 0, count)
	for i := 0; i < count; i++ {
		id := strconv.Itoa(year) + "-" + strconv.Itoa(i)
		out = append(out, Track{ID: id, Artist: "artist " + id, Year: year, PlayCount: plays})
	}
	return out
}

// eightDecades is a library where the 1950s to the 2020s can all fill a mix,
// ranked by plays 2020s > 2010s > ... > 1950s, plus the 1940s with only three
// tracks, which can never fill one.
func eightDecades() []Track {
	var tracks []Track
	for i, y := range []int{1955, 1965, 1975, 1985, 1995, 2005, 2015, 2025} {
		tracks = append(tracks, decadeTracks(y, 12, i+1)...)
	}
	tracks = append(tracks, decadeTracks(1945, 3, 100)...)
	return tracks
}

// Empty settings must be today's behaviour exactly: same decades, same order.
func TestDecadePoolWithNoChoicesIsTopDecadesOwning(t *testing.T) {
	tracks := eightDecades()
	want := TopDecadesOwning(tracks, 6, 10, 3)
	for _, c := range []struct {
		name             string
		include, exclude []int
	}{
		{"nil", nil, nil},
		{"empty", []int{}, []int{}},
	} {
		if got := DecadePool(tracks, 6, 10, 3, c.include, c.exclude); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: DecadePool = %v, want TopDecadesOwning %v", c.name, got, want)
		}
	}
	if !reflect.DeepEqual(want, []int{2020, 2010, 2000, 1990, 1980, 1970}) {
		t.Fatalf("fixture drifted: TopDecadesOwning = %v", want)
	}
}

func TestDecadePool(t *testing.T) {
	tracks := eightDecades()
	for _, c := range []struct {
		name             string
		include, exclude []int
		want             []int
	}{
		{
			name:    "exclude removes a decade and the next one moves up",
			exclude: []int{2020},
			want:    []int{2010, 2000, 1990, 1980, 1970, 1960},
		},
		{
			name:    "include pulls a decade from outside the top 6 to the front",
			include: []int{1950},
			want:    []int{1950, 2020, 2010, 2000, 1990, 1980},
		},
		{
			name:    "included decades come first in ascending order, written in any order",
			include: []int{1960, 1950},
			want:    []int{1950, 1960, 2020, 2010, 2000, 1990},
		},
		{
			name:    "an included decade already in the top 6 is not listed twice",
			include: []int{2010},
			want:    []int{2010, 2020, 2000, 1990, 1980, 1970},
		},
		{
			name:    "an included decade that cannot fill a mix is left out",
			include: []int{1940},
			want:    []int{2020, 2010, 2000, 1990, 1980, 1970},
		},
		{
			name:    "an included decade with no tracks at all is left out",
			include: []int{1920},
			want:    []int{2020, 2010, 2000, 1990, 1980, 1970},
		},
		{
			name:    "exclude wins over include",
			include: []int{1950},
			exclude: []int{1950},
			want:    []int{2020, 2010, 2000, 1990, 1980, 1970},
		},
		{
			name:    "more included decades than 6 makes the pool that big",
			include: []int{1950, 1960, 1970, 1980, 1990, 2000, 2010},
			want:    []int{1950, 1960, 1970, 1980, 1990, 2000, 2010},
		},
		{
			name:    "include and exclude together",
			include: []int{1950},
			exclude: []int{2020, 2010},
			want:    []int{1950, 2000, 1990, 1980, 1970, 1960},
		},
		{
			name:    "excluding almost everything leaves a short pool",
			exclude: []int{2020, 2010, 2000, 1990, 1980, 1970, 1960},
			want:    []int{1950},
		},
		{
			name:    "duplicates in include count once",
			include: []int{1950, 1950},
			want:    []int{1950, 2020, 2010, 2000, 1990, 1980},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DecadePool(tracks, 6, 10, 3, c.include, c.exclude); !reflect.DeepEqual(got, c.want) {
				t.Errorf("DecadePool(include %v, exclude %v) = %v, want %v", c.include, c.exclude, got, c.want)
			}
		})
	}
}

// An included decade is still a decade a mix can be built from: the rotation
// over the pool with it in front puts it in the first week's window.
func TestIncludedDecadeReachesTheRotation(t *testing.T) {
	pool := DecadePool(eightDecades(), 6, 10, 3, []int{1950}, nil)
	seen := map[int]bool{}
	for week := 0; week < len(pool); week++ {
		for _, d := range Rotate(pool, week, 2) {
			seen[d] = true
		}
	}
	if !seen[1950] {
		t.Fatalf("1950 never rotated in over %d weeks, pool %v", len(pool), pool)
	}
}
