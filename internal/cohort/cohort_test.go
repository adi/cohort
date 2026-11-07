package cohort

import (
	"io/fs"
	"testing"
	"time"

	"cohort/internal/tmux"
)

func TestHasWindow(t *testing.T) {
	wins := []tmux.Window{{Index: 0, Name: "a"}, {Index: 3, Name: "b"}}
	if !hasWindow(wins, "a") || !hasWindow(wins, "b") {
		t.Error("existing windows not found")
	}
	if hasWindow(wins, "c") {
		t.Error("missing window found")
	}
	if hasWindow(nil, "a") {
		t.Error("nil slice matched")
	}
}

func TestFirstFreeIndex(t *testing.T) {
	cases := []struct {
		wins []tmux.Window
		want int
	}{
		{nil, 0},
		{[]tmux.Window{{Index: 0}}, 1},
		{[]tmux.Window{{Index: 1, Name: "m"}}, 0},
		{[]tmux.Window{{Index: 0}, {Index: 2}}, 1},
		{[]tmux.Window{{Index: 0}, {Index: 1}, {Index: 2}}, 3},
	}
	for _, c := range cases {
		if got := firstFreeIndex(c.wins); got != c.want {
			t.Errorf("firstFreeIndex(%v) = %d, want %d", c.wins, got, c.want)
		}
	}
}

func TestLastIndex(t *testing.T) {
	if got := lastIndex(nil); got != -1 {
		t.Errorf("lastIndex(nil) = %d", got)
	}
	wins := []tmux.Window{{Index: 5}, {Index: 2}, {Index: 9}}
	if got := lastIndex(wins); got != 9 {
		t.Errorf("lastIndex = %d", got)
	}
}

func TestStampDistinguishesFiles(t *testing.T) {
	a := fakeInfo{mtime: 100, size: 1}
	b := fakeInfo{mtime: 100, size: 2}
	c := fakeInfo{mtime: 101, size: 1}
	if stamp(a) != stamp(a) {
		t.Error("same file, different stamp")
	}
	if stamp(a) == stamp(b) || stamp(a) == stamp(c) {
		t.Error("different files, same stamp")
	}
	if stamp(nil) != 0 {
		t.Error("nil stamp")
	}
}

type fakeInfo struct {
	mtime int64
	size  int64
}

func (f fakeInfo) Name() string       { return "f" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Unix(f.mtime, 0) }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }
