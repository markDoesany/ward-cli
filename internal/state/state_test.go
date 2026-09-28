package state

import "testing"

func TestSaveAndLoad(t *testing.T) {
	store := New(t.TempDir())
	want := State{Project: "sample", Environment: "development"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
