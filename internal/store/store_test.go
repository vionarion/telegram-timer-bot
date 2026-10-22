package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPresets(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SavePreset(ctx, Preset{ChatID: 1, Name: "чай", Length: 3 * time.Minute}))
	must(st.SavePreset(ctx, Preset{ChatID: 1, Name: "помидор", Length: 25 * time.Minute}))
	must(st.SavePreset(ctx, Preset{ChatID: 2, Name: "чай", Length: 5 * time.Minute}))
	// saving again updates the length
	must(st.SavePreset(ctx, Preset{ChatID: 1, Name: "чай", Length: 4 * time.Minute}))

	list, err := st.ListPresets(ctx, 1)
	must(err)
	if len(list) != 2 || list[0].Name != "чай" || list[0].Length != 4*time.Minute {
		t.Errorf("list: %+v", list)
	}

	p, err := st.GetPreset(ctx, 2, "чай")
	must(err)
	if p.Length != 5*time.Minute {
		t.Errorf("chat 2 preset: %+v", p)
	}

	must(st.DeletePreset(ctx, 1, "чай"))
	if _, err := st.GetPreset(ctx, 1, "чай"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted preset still found: %v", err)
	}
	if err := st.DeletePreset(ctx, 1, "нет такого"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
}
