package agent

import (
	"reflect"
	"testing"
)

func TestMergePanelURLsKeepsWorkingAddress(t *testing.T) {
	got := mergePanelURLs([]string{"https://panel.example.com/", "https://backup.example.com"}, "http://172.17.0.1:18080")
	want := []string{"https://panel.example.com", "https://backup.example.com", "http://172.17.0.1:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	got = mergePanelURLs([]string{"https://panel.example.com"}, "https://panel.example.com/")
	if len(got) != 1 {
		t.Fatalf("duplicate kept: %v", got)
	}
	if got = mergePanelURLs(nil, "http://a"); !reflect.DeepEqual(got, []string{"http://a"}) {
		t.Fatalf("empty list: %v", got)
	}
}
