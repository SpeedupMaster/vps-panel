package api

import "testing"

func TestWithBase(t *testing.T) {
	page := []byte("<html><head><meta charset=\"utf-8\"></head></html>")
	for prefix, want := range map[string]string{
		"/":       `<html><head><base href="/"><meta charset="utf-8"></head></html>`,
		"/secret": `<html><head><base href="/secret/"><meta charset="utf-8"></head></html>`,
		`/a"b`:    `<html><head><base href="/a&#34;b/"><meta charset="utf-8"></head></html>`,
	} {
		if got := string(withBase(page, prefix)); got != want {
			t.Errorf("withBase(%q) = %s, want %s", prefix, got, want)
		}
	}
}
