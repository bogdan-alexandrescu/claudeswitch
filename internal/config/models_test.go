package config

import (
	"reflect"
	"strings"
	"testing"
)

// IMPROVEMENTS I6: `models` names the models whose weekly limits count like
// the weekly window. Global, and per profile. Empty is today's behaviour.

func TestModelsDefaultToNone(t *testing.T) {
	c := mustLoad(t, profileAccounts)
	if len(c.Models) != 0 || len(c.ForProfile(DefaultProfile).Models) != 0 {
		t.Fatalf("models default = %v, want none", c.Models)
	}
}

func TestModelsGlobalAndPerProfile(t *testing.T) {
	c := mustLoad(t, `models = ["Modelname"]`+"\n"+profileAccounts+`
[[profile]]
name = "default"
pool = ["personal", "a4"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["work-1", "work-2"]
models = ["Othermodel", "Modelname"]

[[profile]]
name = "plain"
dir  = "~/.claude-plain"
pool = []
models = []
`)
	if got := c.ForProfile("default").Models; !reflect.DeepEqual(got, []string{"Modelname"}) {
		t.Errorf("default inherits the global list, got %v", got)
	}
	if got := c.ForProfile("work").Models; !reflect.DeepEqual(got, []string{"Othermodel", "Modelname"}) {
		t.Errorf("work's own list wins, got %v", got)
	}
	if got := c.ForProfile("plain").Models; len(got) != 0 {
		t.Errorf("an explicit empty list turns the global one off for that profile, got %v", got)
	}
	if !c.ForProfile("work").CountsModel("othermodel") {
		t.Error("CountsModel matches names case-insensitively")
	}
	if c.ForProfile("plain").CountsModel("Modelname") {
		t.Error("CountsModel on a profile that counts none")
	}
}

func TestModelsRejectsABlankName(t *testing.T) {
	_, err := loadString(t, `models = ["Modelname", "  "]`+"\n"+profileAccounts)
	if err == nil || !strings.Contains(err.Error(), "models") {
		t.Fatalf("a blank model name must be refused, got %v", err)
	}
}
