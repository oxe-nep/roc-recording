package commentator

import "testing"

func TestStoreOutputFormat(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/commentator-settings.json")
	store.Ensure([]int{1})

	got, err := store.Set(1, SettingsUpdateInput{
		OutputFormat: strPtr("Hp50"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputFormat != "Hp50" {
		t.Fatalf("expected Hp50, got %q", got.OutputFormat)
	}
	if store.Get(1).OutputFormat != "Hp50" {
		t.Fatalf("Get after Set: %q", store.Get(1).OutputFormat)
	}
}

func strPtr(s string) *string { return &s }
