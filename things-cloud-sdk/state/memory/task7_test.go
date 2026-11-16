package memory

import (
	"encoding/json"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

// Synthetic fixtures (no real account data): Things 3.23+ writes Task7
// envelopes with the same property keys as Task6 and explicit nulls for
// cleared fields.

func TestTask7CreateThenTask6ModifyAndNullClears(t *testing.T) {
	s := NewState()
	create := json.RawMessage(`{"tt":"Task7 item","tp":0,"ss":0,"st":1,"sr":1794700800,"tir":1794700800,"ar":["area-1"],"pr":null,"agr":null,"rt":[],"nt":null,"cd":1794600000,"md":null}`)
	if err := s.Update(things.Item{UUID: "t7", Kind: things.ItemKindTask7, Action: things.ItemActionCreated, P: create}); err != nil {
		t.Fatalf("Task7 create rejected: %v", err)
	}
	task := s.Tasks["t7"]
	if task == nil || task.Title != "Task7 item" {
		t.Fatalf("Task7 create not applied: %#v", task)
	}
	if task.ScheduledDate == nil || len(task.AreaIDs) != 1 {
		t.Fatalf("Task7 fields not decoded: sr=%v ar=%v", task.ScheduledDate, task.AreaIDs)
	}

	// A Task6 modification of a Task7 task (what this server writes) applies.
	if err := s.Update(things.Item{UUID: "t7", Kind: things.ItemKindTask, Action: things.ItemActionModified, P: json.RawMessage(`{"tt":"Renamed"}`)}); err != nil {
		t.Fatalf("Task6 modify rejected: %v", err)
	}
	if s.Tasks["t7"].Title != "Renamed" || s.Tasks["t7"].ScheduledDate == nil {
		t.Fatal("sparse Task6 modification must keep omitted fields")
	}

	// Task7 explicit nulls clear the schedule date and area; tir is separate.
	clear := json.RawMessage(`{"sr":null,"ar":null,"md":1794700900}`)
	if err := s.Update(things.Item{UUID: "t7", Kind: things.ItemKindTask7, Action: things.ItemActionModified, P: clear}); err != nil {
		t.Fatalf("Task7 modify rejected: %v", err)
	}
	task = s.Tasks["t7"]
	if task.ScheduledDate != nil || task.AreaIDs != nil {
		t.Fatalf("explicit nulls not applied: sr=%v ar=%v", task.ScheduledDate, task.AreaIDs)
	}
	if task.TodayIndexRefDate == nil {
		t.Fatal("clearing sr must not clear tir")
	}

	if err := s.Update(things.Item{UUID: "t7", Kind: things.ItemKindTask7, Action: things.ItemActionDeleted, P: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Task7 delete rejected: %v", err)
	}
	if s.Tasks["t7"] != nil {
		t.Fatal("Task7 delete not applied")
	}
}

func TestFutureTaskKindStillFailsClosed(t *testing.T) {
	s := NewState()
	err := s.Update(
		things.Item{UUID: "ok", Kind: things.ItemKindTask7, Action: things.ItemActionCreated, P: json.RawMessage(`{"tt":"ok"}`)},
		things.Item{UUID: "future", Kind: things.ItemKind("Task8"), Action: things.ItemActionCreated, P: json.RawMessage(`{"tt":"x"}`)},
	)
	if err == nil {
		t.Fatal("unknown task kind must be rejected")
	}
	if len(s.Tasks) != 0 {
		t.Fatal("a rejected batch must not partially apply")
	}
}
