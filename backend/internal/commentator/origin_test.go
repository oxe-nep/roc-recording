package commentator

import (
	"net/http/httptest"
	"testing"
)

func TestDeckClaimOriginPrefersConfiguredPublicURL(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/commentator/deck/claim", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "commentator.example.test")

	got := deckClaimOrigin("https://commentator.example.test", req)
	want := "https://commentator.example.test"
	if got != want {
		t.Fatalf("deckClaimOrigin = %q, want %q", got, want)
	}
}

func TestDeckClaimOriginFallsBackToRequestWhenConfiguredIsLocalhost(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/commentator/deck/claim", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "commentator.example.test")

	got := deckClaimOrigin("http://127.0.0.1:8080", req)
	want := "https://commentator.example.test"
	if got != want {
		t.Fatalf("deckClaimOrigin = %q, want %q", got, want)
	}
}
