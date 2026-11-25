package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func TestRootHandlerBlocksGRPCOnSubOnlyHost(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := secret.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := service.New(st, box, auth.New(st, box), hub.New(st, box), dir)
	_ = st.Set("panel_url", "https://panel.example.com")
	_ = st.Set("sub_url", "https://sub.example.com")

	grpcHit := false
	grpcStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grpcHit = true
		w.WriteHeader(http.StatusOK)
	})
	ginStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	root := newRootHandler(p, grpcStub, ginStub)

	// HTTP/2 gRPC request targeting SubOnlyHost must get 404 and never reach grpcSrv.
	req := httptest.NewRequest(http.MethodPost, "/vpspanel.AgentHub/Register", nil)
	req.ProtoMajor = 2
	req.Host = "sub.example.com:443"
	req.Header.Set("Content-Type", "application/grpc")
	w := httptest.NewRecorder()
	root.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || grpcHit {
		t.Fatalf("gRPC on sub host = %d (grpcHit=%v), want 404 and false", w.Code, grpcHit)
	}

	// HTTP/2 gRPC request targeting panel host reaches grpcSrv.
	req2 := httptest.NewRequest(http.MethodPost, "/vpspanel.AgentHub/Register", nil)
	req2.ProtoMajor = 2
	req2.Host = "panel.example.com"
	req2.Header.Set("Content-Type", "application/grpc")
	w2 := httptest.NewRecorder()
	root.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK || !grpcHit {
		t.Fatalf("gRPC on panel host = %d (grpcHit=%v), want 200 and true", w2.Code, grpcHit)
	}
}
