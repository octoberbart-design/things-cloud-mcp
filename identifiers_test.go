package main

import (
	"encoding/json"
	"strings"
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

// productionCrashID is the 15-byte identifier that the old generateUUID()
// wrote for a heading on 2026-11-23; Things.app crashed while syncing it.
const productionCrashID = "5S5pv8jBMDq6DfF42EEhB"

// fixtureID maps a readable fixture name to a deterministic, canonical
// 16-byte Base58 identifier for tests that commit through writeAndSync.
func fixtureID(name string) string {
	return thingscloud.EncodeLegacyIdentifier(name)
}

func TestGenerateUUIDAlwaysDecodesToSixteenBytes(t *testing.T) {
	// ~1/256 random UUIDs start with a zero byte; 300k draws exercise that
	// path about 1,170 times.
	leadingOne := 0
	for i := 0; i < 300000; i++ {
		id := generateUUID()
		if err := thingscloud.ValidateUUID(id); err != nil {
			t.Fatalf("generateUUID() = %q: %v", id, err)
		}
		if strings.HasPrefix(id, "1") {
			leadingOne++
		}
	}
	if leadingOne == 0 {
		t.Fatal("no identifier with a leading zero byte was generated; leading-zero path untested")
	}
}

func TestProductionCrashIDIsInvalid(t *testing.T) {
	if err := thingscloud.ValidateUUID(productionCrashID); err == nil {
		t.Fatalf("%s must be rejected", productionCrashID)
	}
}

func commitItem(t *testing.T, id, kind string, action thingscloud.ItemAction, payload map[string]any) thingscloud.Item {
	t.Helper()
	bs, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return thingscloud.Item{UUID: id, Kind: thingscloud.ItemKind(kind), Action: action, P: bs}
}

func TestValidateCommitIdentifiers(t *testing.T) {
	valid := generateUUID()
	other := generateUUID()
	tests := []struct {
		name    string
		item    thingscloud.Item
		wantErr bool
	}{
		{"valid task with references", commitItem(t, valid, "Task6", 0, map[string]any{"pr": []string{other}, "agr": []string{}, "tg": []string{other}}), false},
		{"valid checklist", commitItem(t, valid, "ChecklistItem3", 0, map[string]any{"ts": []string{other}}), false},
		{"null references", commitItem(t, valid, "Task6", 1, map[string]any{"pr": nil, "agr": nil}), false},
		{"empty delete payload", commitItem(t, valid, "ChecklistItem3", 2, map[string]any{}), false},
		{"crash id as object key", commitItem(t, productionCrashID, "Task6", 0, map[string]any{"tp": 2}), true},
		{"crash id as heading reference", commitItem(t, valid, "Task6", 0, map[string]any{"agr": []string{productionCrashID}}), true},
		{"crash id as checklist parent", commitItem(t, valid, "ChecklistItem3", 0, map[string]any{"ts": []string{productionCrashID}}), true},
		{"hyphenated UUID reference", commitItem(t, valid, "Task6", 1, map[string]any{"pr": []string{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"}}), true},
		{"non-Base58 character", commitItem(t, "0OIl"+valid[4:], "Tag4", 0, map[string]any{}), true},
		{"reference field not a list", commitItem(t, valid, "Task6", 1, map[string]any{"pr": "x"}), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCommitIdentifiers([]thingscloud.Item{test.item})
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestWriteAndSyncRejectsMalformedIdentifierWithoutCommit(t *testing.T) {
	fc := newFakeCloud("test@example.com")
	defer fc.Close()
	tmcp := newTestThingsMCP(t, fc)

	payload := newTaskCreatePayload("heading", map[string]string{"type": "heading"}, 0)
	err := tmcp.writeAndSync(writeEnvelope{id: productionCrashID, action: 0, kind: "Task6", payload: payload})
	if err == nil {
		t.Fatal("writeAndSync accepted a 15-byte identifier")
	}
	if got := len(fc.getCommitLog()); got != 0 {
		t.Fatalf("%d commit(s) reached Things Cloud; want 0", got)
	}
}
