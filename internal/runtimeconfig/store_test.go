package runtimeconfig

import (
	"path/filepath"
	"testing"
)

func TestSaveLostResponseReplayAndConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	s, e := Open(path, Config{})
	if e != nil {
		t.Fatal(e)
	}
	p := Patch{SupportUsername: "support_one"}
	if e = s.Save(p); e != nil {
		t.Fatal(e)
	}
	if e = s.Save(p); e != nil {
		t.Fatal("same save should replay", e)
	}
	if s.Current().Revision != 1 {
		t.Fatal("replay advanced revision")
	}
	reopened, e := Open(path, Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.Save(p); e != nil {
		t.Fatal("replay after restart", e)
	}
	p.SupportUsername = "support_two"
	if e = reopened.Save(p); e == nil {
		t.Fatal("stale change overwrote saved config")
	}
	if reopened.Current().SupportUsername != "support_one" {
		t.Fatal("saved value lost")
	}
}

func TestMuteOnClosePersistsAndDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	s, err := Open(path, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Current().MuteOnClose {
		t.Fatal("must default off")
	}
	if err = s.Save(Patch{MuteOnClose: true}); err == nil {
		t.Fatal("missing group accepted")
	}
	if err = s.Save(Patch{MuteOnClose: true, GroupID: -88}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Current().MuteOnClose || s.View()["mute_on_close"] != true {
		t.Fatal("toggle lost after restart")
	}
}
