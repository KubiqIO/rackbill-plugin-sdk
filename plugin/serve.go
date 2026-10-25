package plugin

import (
	"context"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Serving: what every Serve* entry point shares beyond its kind's service.

var serveSetupOnce sync.Once

// serveSetup runs once per process before serving: the standard library's
// log package is routed through Log so its lines keep a level.
func serveSetup() {
	serveSetupOnce.Do(redirectStdLog)
}

// grpcServer is the GRPCServer every Serve* passes to go-plugin: the
// default server plus the interceptors that apply the agent's log level
// from call metadata and record each RPC at debug.
func grpcServer(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts,
		grpc.ChainUnaryInterceptor(unaryServerInterceptor),
		grpc.ChainStreamInterceptor(streamServerInterceptor),
	)
	return goplugin.DefaultGRPCServer(opts)
}

// applyLevelFromContext adopts the level the agent attached to the call,
// if any. Comparing first keeps the common case — nothing changed — free.
func applyLevelFromContext(ctx context.Context) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return
	}
	vals := md.Get(MetadataLogLevel)
	if len(vals) == 0 {
		return
	}
	if lvl, ok := ParseLogLevel(vals[len(vals)-1]); ok && lvl != LogLevel() {
		_ = SetLogLevel(lvl)
	}
}

func unaryServerInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	applyLevelFromContext(ctx)
	start := time.Now()
	resp, err := handler(ctx, req)
	logRPC(info.FullMethod, time.Since(start), err)
	return resp, err
}

func streamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	applyLevelFromContext(ss.Context())
	start := time.Now()
	err := handler(srv, ss)
	logRPC(info.FullMethod, time.Since(start), err)
	return err
}

// logRPC records one call from the agent at debug — the "what was I asked"
// half of a validation trace, beside the HTTP exchanges the call caused.
func logRPC(fullMethod string, dur time.Duration, err error) {
	if !DebugEnabled() {
		return
	}
	method := fullMethod
	if i := lastIndexByte(fullMethod, '/'); i >= 0 {
		method = fullMethod[i+1:]
	}
	if err != nil {
		Log.Debug("rpc "+method+" failed: "+err.Error(),
			"rpc", method, "duration_ms", dur.Milliseconds(), "code", status.Code(err).String())
		return
	}
	Log.Debug("rpc "+method+" ok", "rpc", method, "duration_ms", dur.Milliseconds())
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
