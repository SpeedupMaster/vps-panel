// Command vps-panel is the management panel.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"vpspanel/internal/common/version"
	"vpspanel/internal/panel/api"
	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/webui"
	"vpspanel/scripts"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	tuneMemory()
	if handled, err := signCommand(os.Args[1:]); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	dataDir := flag.String("data", env("VPSPANEL_DATA", "./data"), "data directory")
	listen := flag.String("listen", env("VPSPANEL_LISTEN", ":8080"), "listen address (HTTP + gRPC)")
	adminUser := flag.String("admin", env("VPSPANEL_ADMIN", "admin"), "admin username on first start")
	adminPass := flag.String("password", env("VPSPANEL_PASSWORD", ""), "admin password on first start (random when empty)")
	panelURL := flag.String("url", env("VPSPANEL_URL", ""), "public panel URL, e.g. https://panel.example.com")
	tlsCert := flag.String("tls-cert", env("VPSPANEL_TLS_CERT", ""), "TLS certificate (omit when behind Caddy)")
	tlsKey := flag.String("tls-key", env("VPSPANEL_TLS_KEY", ""), "TLS key")
	trustedProxies := flag.String("trusted-proxies", env("VPSPANEL_TRUSTED_PROXIES", "127.0.0.1,::1"), "comma-separated trusted reverse proxy IPs/CIDRs")
	showVer := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVer {
		println(version.Version)
		return
	}
	log.Printf("vps-panel %s (%s)", version.Version, version.Commit)

	if err := service.ApplyPendingRestore(*dataDir); err != nil {
		log.Fatalf("restore: %v", err)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	box, err := secret.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if *panelURL != "" {
		_ = st.Set("panel_url", strings.TrimRight(*panelURL, "/"))
	}
	au := auth.New(st, box)
	if pw, err := au.EnsureAdmin(*adminUser, *adminPass); err != nil {
		log.Fatal(err)
	} else if pw != "" && *adminPass == "" {
		log.Printf("首次启动：管理员 %s，初始密码 %s（登录后请立即修改并开启两步验证）", *adminUser, pw)
	}

	agentSrc := bundledAgentDir(env("VPSPANEL_AGENT_DIR", ""))

	trusted := splitCSV(*trustedProxies)
	h := hub.New(st, box)
	h.SetTrustedProxies(trusted)
	p := service.New(st, box, au, h, *dataDir)
	p.RegisterBundledAgent(agentSrc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &api.API{P: p, Web: webui.FS(), Script: scripts.Install, TrustedProxies: trusted}
	modules := setup(ctx, p, a)
	sch := scheduler.New(ctx)
	p.StartJobs(sch)
	for _, m := range modules {
		m(sch)
	}

	grpcSrv := h.GRPCServer()
	ginEngine := a.Router()
	mux := newRootHandler(p, grpcSrv, ginEngine)
	srv := &http.Server{Addr: *listen, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	if *tlsCert != "" {
		srv.Handler = mux
		_ = http2.ConfigureServer(srv, &http2.Server{})
	} else {
		srv.Handler = h2c.NewHandler(mux, &http2.Server{}) // Caddy: reverse_proxy h2c://127.0.0.1:8080
	}

	go func() {
		log.Printf("listening on %s", *listen)
		var err error
		if *tlsCert != "" {
			err = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	log.Print("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grpcSrv.Stop()
	_ = srv.Shutdown(sctx)
	_ = st.Close()
}

func newRootHandler(p *service.Panel, grpcSrv, ginEngine http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			if subHost := p.SubOnlyHost(); subHost != "" && hostOnly(r.Host) == subHost {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			grpcSrv.ServeHTTP(w, r)
			return
		}
		ginEngine.ServeHTTP(w, r)
	})
}

func hostOnly(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
