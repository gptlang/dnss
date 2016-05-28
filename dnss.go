package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"blitiri.com.ar/go/dnss/internal/dnstox"
	"blitiri.com.ar/go/dnss/internal/grpctodns"

	"github.com/golang/glog"
	"google.golang.org/grpc"

	// Register pprof handlers for monitoring and debugging.
	_ "net/http/pprof"

	// Make GRPC log to glog.
	_ "google.golang.org/grpc/grpclog/glogger"
)

var (
	dnsListenAddr = flag.String("dns_listen_addr", ":53",
		"address to listen on for DNS")

	dnsUnqualifiedUpstream = flag.String("dns_unqualified_upstream", "",
		"DNS server to forward unqualified requests to")

	fallbackUpstream = flag.String("fallback_upstream", "8.8.8.8:53",
		"DNS server to resolve domains in --fallback_domains")
	fallbackDomains = flag.String("fallback_domains", "dns.google.com.",
		"Domains we resolve via DNS, using --fallback_upstream"+
			" (space-separated list)")

	enableDNStoGRPC = flag.Bool("enable_dns_to_grpc", false,
		"enable DNS-to-GRPC server")
	grpcUpstream = flag.String("grpc_upstream", "localhost:9953",
		"address of the upstream GRPC server")
	grpcClientCAFile = flag.String("grpc_client_cafile", "",
		"CA file to use for the GRPC client")

	enableGRPCtoDNS = flag.Bool("enable_grpc_to_dns", false,
		"enable GRPC-to-DNS server")
	grpcListenAddr = flag.String("grpc_listen_addr", ":9953",
		"address to listen on for GRPC")
	dnsUpstream = flag.String("dns_upstream", "8.8.8.8:53",
		"address of the upstream DNS server")

	enableDNStoHTTPS = flag.Bool("enable_dns_to_https", false,
		"enable DNS-to-HTTPS proxy")
	httpsUpstream = flag.String("https_upstream",
		"https://dns.google.com/resolve",
		"URL of upstream DNS-to-HTTP server")
	httpsClientCAFile = flag.String("https_client_cafile", "",
		"CA file to use for the HTTPS client")

	grpcCert = flag.String("grpc_cert", "",
		"certificate file for the GRPC server")
	grpcKey = flag.String("grpc_key", "",
		"key file for the GRPC server")

	logFlushEvery = flag.Duration("log_flush_every", 30*time.Second,
		"how often to flush logs")
	monitoringListenAddr = flag.String("monitoring_listen_addr", "",
		"address to listen on for monitoring HTTP requests")
)

func flushLogs() {
	c := time.Tick(*logFlushEvery)
	for range c {
		glog.Flush()
	}
}

func main() {
	defer glog.Flush()

	flag.Parse()

	go flushLogs()

	grpc.EnableTracing = false
	if *monitoringListenAddr != "" {
		launchMonitoringServer(*monitoringListenAddr)
	}

	if !*enableDNStoGRPC && !*enableGRPCtoDNS && !*enableDNStoHTTPS {
		glog.Error("Need to set one of the following:")
		glog.Error("  --enable_dns_to_https")
		glog.Error("  --enable_dns_to_grpc")
		glog.Error("  --enable_grpc_to_dns")
		glog.Fatal("")
	}

	if *enableDNStoGRPC && *enableDNStoHTTPS {
		glog.Error("The following options cannot be set at the same time:")
		glog.Error("  --enable_dns_to_grpc and --enable_dns_to_https")
		glog.Fatal("")
	}

	var wg sync.WaitGroup

	// DNS to GRPC.
	if *enableDNStoGRPC {
		r := dnstox.NewGRPCResolver(*grpcUpstream, *grpcClientCAFile)
		cr := dnstox.NewCachingResolver(r)
		dtg := dnstox.New(*dnsListenAddr, cr, *dnsUnqualifiedUpstream)
		dtg.SetFallback(
			*fallbackUpstream, strings.Split(*fallbackDomains, " "))
		wg.Add(1)
		go func() {
			defer wg.Done()
			dtg.ListenAndServe()
		}()
	}

	// GRPC to DNS.
	if *enableGRPCtoDNS {
		gtd := &grpctodns.Server{
			Addr:     *grpcListenAddr,
			Upstream: *dnsUpstream,
			CertFile: *grpcCert,
			KeyFile:  *grpcKey,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			gtd.ListenAndServe()
		}()
	}

	// DNS to HTTPS.
	if *enableDNStoHTTPS {
		r := dnstox.NewHTTPSResolver(*httpsUpstream, *httpsClientCAFile)
		cr := dnstox.NewCachingResolver(r)
		dth := dnstox.New(*dnsListenAddr, cr, *dnsUnqualifiedUpstream)
		dth.SetFallback(
			*fallbackUpstream, strings.Split(*fallbackDomains, " "))
		wg.Add(1)
		go func() {
			defer wg.Done()
			dth.ListenAndServe()
		}()
	}

	wg.Wait()
}

func launchMonitoringServer(addr string) {
	glog.Infof("Monitoring HTTP server listening on %s", addr)
	grpc.EnableTracing = true

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(monitoringHTMLIndex))
	})

	flags := dumpFlags()
	http.HandleFunc("/debug/flags", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(flags))
	})

	go http.ListenAndServe(addr, nil)
}

// Static index for the monitoring website.
const monitoringHTMLIndex = `<!DOCTYPE html>
<html>
  <head>
    <title>dnss monitoring</title>
  </head>
  <body>
    <h1>dnss monitoring</h1>
    <ul>
      <li><a href="/debug/requests">requests</a>
          <small><a href="https://godoc.org/golang.org/x/net/trace">
            (ref)</a></small>
        <ul>
          <li><a href="/debug/requests?fam=dnstox&b=11">dnstox latency</a>
          <li><a href="/debug/requests?fam=dnstox&b=0&exp=1">dnstox trace</a>
        </ul>
      <li><a href="/debug/dnstox/cache/dump">cache dump</a>
      <li><a href="/debug/pprof">pprof</a>
          <small><a href="https://golang.org/pkg/net/http/pprof/">
            (ref)</a></small>
        <ul>
          <li><a href="/debug/pprof/goroutine?debug=1">goroutines</a>
        </ul>
      <li><a href="/debug/flags">flags</a>
      <li><a href="/debug/vars">public variables</a>
    </ul>
  </body>
</html>
`

// dumpFlags to a string, for troubleshooting purposes.
func dumpFlags() string {
	s := ""
	visited := make(map[string]bool)

	// Print set flags first, then the rest.
	flag.Visit(func(f *flag.Flag) {
		s += fmt.Sprintf("-%s=%s\n", f.Name, f.Value.String())
		visited[f.Name] = true
	})

	s += "\n"
	flag.VisitAll(func(f *flag.Flag) {
		if !visited[f.Name] {
			s += fmt.Sprintf("-%s=%s\n", f.Name, f.Value.String())
		}
	})

	return s
}
