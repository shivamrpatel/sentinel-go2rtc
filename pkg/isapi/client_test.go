package isapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestSearchClips(t *testing.T) {
	// Simulate ISAPI search endpoint response
	const xmlResponse = `<?xml version='1.0' encoding='UTF-8'?>
<CMSearchResult>
  <numOfResults>2</numOfResults>
  <matchList>
    <searchMatchItem>
      <startTime>2024-06-01T10:00:00Z</startTime>
      <endTime>2024-06-01T10:05:00Z</endTime>
      <playbackURI>http://example.com/clip1.mp4</playbackURI>
    </searchMatchItem>
    <searchMatchItem>
      <startTime>2024-06-01T10:05:00Z</startTime>
      <endTime>2024-06-01T10:10:00Z</endTime>
      <playbackURI>http://example.com/clip2.mp4</playbackURI>
    </searchMatchItem>
  </matchList>
</CMSearchResult>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ISAPI/ContentMgmt/search" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(xmlResponse))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := &Client{url: ts.URL}
	start := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 1, 10, 10, 0, 0, time.UTC)

	clips, err := client.SearchClips("101", start, end, 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips, got %d", len(clips))
	}
	if clips[0].URI != "http://example.com/clip1.mp4" || clips[1].URI != "http://example.com/clip2.mp4" {
		t.Errorf("unexpected clip URIs: %+v", clips)
	}
}

func TestSearchClips_Localhost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manual test")
	}
	username := "admin"      // change as needed
	password := "1215cf9114" // updated password for localhost test
	client := NewClientWithAuth("http://localhost:8085", username, password)
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	clips, err := client.SearchClips("101", start, end, 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}
	for i, clip := range clips {
		t.Logf("Clip %d: start=%s end=%s uri=%s", i, clip.StartTime, clip.EndTime, clip.URI)
	}
}

func TestDownloadClip_Localhost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manual test")
	}
	username := "admin"
	password := "1215cf9114"
	client := NewClientWithAuth("http://localhost:8085", username, password)
	start := time.Now().Add(-24 * time.Hour) // try a wider window for more results
	end := time.Now()

	clips, err := client.SearchClips("101", start, end, 1)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}
	if len(clips) == 0 {
		// Broaden the window to all of July 11th
		start = time.Date(2025, 7, 11, 0, 0, 0, 0, time.UTC)
		end = time.Date(2025, 7, 11, 23, 59, 59, 0, time.UTC)
		clips, err = client.SearchClips("101", start, end, 1)
		if err != nil {
			t.Fatalf("SearchClips (broadened) failed: %v", err)
		}
		if len(clips) == 0 {
			t.Skip("No clips found to download even after broadening window")
		}
	}

	t.Logf("Clips found: %+v", clips)
	t.Logf("First clip URI: %q", clips[0].URI)

	tmpfile := "test_download.mp4"
	err = client.DownloadClipHTTP(clips[0], tmpfile)
	if err != nil {
		t.Fatalf("DownloadClip failed: %v", err)
	}
	info, err := os.Stat(tmpfile)
	if err != nil {
		t.Fatalf("Downloaded file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("Downloaded file is empty")
	}
	os.Remove(tmpfile)
}

func TestStitchAndProcessClips_Localhost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manual test")
	}
	username := "admin"
	password := "1215cf9114"
	client := NewClientWithAuth("http://localhost:8085", username, password)
	// Use a 1-minute time range for fast testing
	start := time.Date(2025, 7, 10, 13, 15, 45, 0, time.UTC)
	end := start.Add(time.Minute)

	clips, err := client.SearchClips("101", start, end, 100)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}
	if len(clips) == 0 {
		t.Skip("No clips found to stitch")
	}

	paths, err := client.DownloadAllClipsToTemp(clips, "stitchtest")
	cleanup := func() { CleanupFiles(paths) }
	if err != nil {
		cleanup()
		t.Fatalf("DownloadAllClipsToTemp failed: %v", err)
	}
	defer cleanup()

	outputFile := "/tmp/test_stitched.mp4"
	err = StitchAndProcessClips(paths, outputFile, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("StitchAndProcessClips failed: %v", err)
	}
	t.Logf("Stitched video written to: %s", outputFile)
	// Do not clean up outputFile so user can inspect it
}
