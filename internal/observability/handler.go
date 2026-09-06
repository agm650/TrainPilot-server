package observability

import (
	"net/http"
	"net/http/pprof"
)

// DiagnosticHandler builds a private mux. It never uses http.DefaultServeMux,
// which prevents diagnostic routes from leaking onto the public API listener.
func DiagnosticHandler(metrics *Metrics, metricsEnabled, pprofEnabled bool) http.Handler {
	mux := http.NewServeMux()
	if metricsEnabled && metrics != nil {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	if pprofEnabled {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
		mux.Handle("GET /debug/pprof/allocs", pprof.Handler("allocs"))
		mux.Handle("GET /debug/pprof/block", pprof.Handler("block"))
		mux.Handle("GET /debug/pprof/goroutine", pprof.Handler("goroutine"))
		mux.Handle("GET /debug/pprof/heap", pprof.Handler("heap"))
		mux.Handle("GET /debug/pprof/mutex", pprof.Handler("mutex"))
		mux.Handle("GET /debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	}
	return mux
}
