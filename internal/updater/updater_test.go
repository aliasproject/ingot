package updater

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// A 200 whose body isn't the release asset (GitHub's JSON metadata, when
// the Accept header is missing) must not be accepted as the new binary.
func TestDownloadFileRejectsWrongSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"name":"ingot-linux-amd64","size":12000000}`))
	}))
	defer srv.Close()

	dest, err := os.CreateTemp(t.TempDir(), "ingot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	u := New()
	if err := u.downloadFile(srv.URL, dest, "", 12000000); err == nil {
		t.Fatal("downloadFile accepted a body of the wrong size")
	}
}

func TestDownloadFileAcceptsExpectedSize(t *testing.T) {
	body := []byte("binary-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest, err := os.CreateTemp(t.TempDir(), "ingot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	u := New()
	if err := u.downloadFile(srv.URL, dest, "", int64(len(body))); err != nil {
		t.Fatalf("downloadFile rejected the expected body: %v", err)
	}
}
