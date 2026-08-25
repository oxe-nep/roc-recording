package commentator

import "testing"

func TestStoreQuality(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/commentator-settings.json")
	store.Ensure([]int{1})

	got, err := store.Set(1, SettingsUpdateInput{
		Quality: &QualitySettings{
			ToCommentatorVideo:   VideoQualityMonitoring,
			ToCommentatorAudio:   AudioQualityVoice,
			FromCommentatorVideo: VideoQualityHigh,
			FromCommentatorAudio: AudioQualityBroadcast,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality.ToCommentatorVideo != VideoQualityMonitoring {
		t.Fatalf("to video = %q", got.Quality.ToCommentatorVideo)
	}
	reloaded := NewStore(dir + "/commentator-settings.json")
	reloaded.Load()
	q := reloaded.Get(1).Quality
	if q.FromCommentatorVideo != VideoQualityHigh || q.ToCommentatorAudio != AudioQualityVoice {
		t.Fatalf("persisted quality: %+v", q)
	}
}

func TestStoreDisplayName(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/commentator-settings.json")
	store.Ensure([]int{1})

	got, err := store.Set(1, SettingsUpdateInput{
		DisplayName: strPtr("Studio A"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Studio A" {
		t.Fatalf("expected Studio A, got %q", got.DisplayName)
	}
	if store.Get(1).DisplayName != "Studio A" {
		t.Fatalf("Get after Set: %q", store.Get(1).DisplayName)
	}
}

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
